package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLastFactor is returned when removing a two-factor method would leave an
// already-enrolled account with none — an enrolled account may never drop
// back to zero factors.
var ErrLastFactor = errors.New("cannot remove the last two-factor method")

// IsTwoFactorEnrolled reports whether userID has completed the mandatory
// 2FA enrollment gate.
func IsTwoFactorEnrolled(ctx context.Context, pool *pgxpool.Pool, userID string) (bool, error) {
	var enrolled bool
	err := pool.QueryRow(ctx, `SELECT two_factor_enrolled_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&enrolled)
	return enrolled, err
}

// markEnrolledIfFirst sets users.two_factor_enrolled_at the first time any
// factor is confirmed; idempotent on later calls.
func markEnrolledIfFirst(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET two_factor_enrolled_at = COALESCE(two_factor_enrolled_at, now()) WHERE id = $1`, userID)
	return err
}

// SetupTOTP stores a new, unconfirmed TOTP secret for userID, replacing any
// prior unconfirmed setup (a fresh QR code invalidates the last one shown).
func SetupTOTP(ctx context.Context, pool *pgxpool.Pool, userID string, encryptedSecret []byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_totp (user_id, encrypted_secret, confirmed_at)
		VALUES ($1, $2, NULL)
		ON CONFLICT (user_id) DO UPDATE SET encrypted_secret = EXCLUDED.encrypted_secret, confirmed_at = NULL
	`, userID, encryptedSecret)
	return err
}

// GetTOTP returns userID's stored (sealed) TOTP secret and whether it has
// been confirmed. Returns ErrNotFound if TOTP was never set up.
func GetTOTP(ctx context.Context, pool *pgxpool.Pool, userID string) (encryptedSecret []byte, confirmed bool, err error) {
	var confirmedAt *time.Time
	err = pool.QueryRow(ctx, `SELECT encrypted_secret, confirmed_at FROM user_totp WHERE user_id = $1`, userID).Scan(&encryptedSecret, &confirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	return encryptedSecret, confirmedAt != nil, nil
}

// ConfirmTOTP marks userID's TOTP setup confirmed and, if this is the
// account's first confirmed factor, completes the mandatory enrollment gate.
func ConfirmTOTP(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	tag, err := pool.Exec(ctx, `UPDATE user_totp SET confirmed_at = now() WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return markEnrolledIfFirst(ctx, pool, userID)
}

// DeleteTOTP removes userID's TOTP factor, refusing (ErrLastFactor) if the
// account is enrolled and this is its only remaining factor.
func DeleteTOTP(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	return removeFactor(ctx, pool, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM user_totp WHERE user_id = $1`, userID)
		return err
	})
}

// EnableEmailTwoFactor turns on email-OTP as a factor for userID, completing
// enrollment if it's the account's first confirmed factor.
func EnableEmailTwoFactor(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	if _, err := pool.Exec(ctx, `INSERT INTO user_email_2fa (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return err
	}
	return markEnrolledIfFirst(ctx, pool, userID)
}

// IsEmailTwoFactorEnabled reports whether userID has email-OTP enabled.
func IsEmailTwoFactorEnabled(ctx context.Context, pool *pgxpool.Pool, userID string) (bool, error) {
	var enabled bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_email_2fa WHERE user_id = $1)`, userID).Scan(&enabled)
	return enabled, err
}

// DisableEmailTwoFactor removes email-OTP as a factor for userID, refusing
// (ErrLastFactor) if the account is enrolled and this is its only remaining
// factor.
func DisableEmailTwoFactor(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	return removeFactor(ctx, pool, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM user_email_2fa WHERE user_id = $1`, userID)
		return err
	})
}

// factorCount returns how many independent 2FA methods userID currently has
// (confirmed TOTP + email-OTP + WebAuthn credentials). Backup codes are a
// recovery aid for an existing factor, not counted as one themselves.
func factorCount(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM user_totp WHERE user_id = $1 AND confirmed_at IS NOT NULL) +
			(SELECT count(*) FROM user_email_2fa WHERE user_id = $1) +
			(SELECT count(*) FROM webauthn_credentials WHERE user_id = $1)
	`, userID).Scan(&n)
	return n, err
}

// removeFactor runs del (a DELETE against one factor table) inside a
// transaction, refusing with ErrLastFactor if the account is enrolled and
// deleting would leave zero factors.
func removeFactor(ctx context.Context, pool *pgxpool.Pool, userID string, del func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	enrolled, err := IsTwoFactorEnrolled(ctx, pool, userID)
	if err != nil {
		return err
	}
	before, err := factorCount(ctx, pool, userID)
	if err != nil {
		return err
	}
	if err := del(tx); err != nil {
		return err
	}
	if enrolled && before <= 1 {
		return ErrLastFactor
	}
	return tx.Commit(ctx)
}

// GenerateAndStoreBackupCodes replaces userID's backup codes with a fresh
// set of 10, returning the plaintext codes (shown to the user exactly once;
// only their hashes are persisted).
func GenerateAndStoreBackupCodes(ctx context.Context, pool *pgxpool.Pool, userID string) ([]string, error) {
	codes, err := GenerateBackupCodes()
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM user_backup_codes WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	for _, code := range codes {
		if _, err := tx.Exec(ctx, `INSERT INTO user_backup_codes (user_id, code_hash) VALUES ($1, $2)`, userID, HashToken(code)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

// CountUnusedBackupCodes returns how many of userID's backup codes remain
// unused.
func CountUnusedBackupCodes(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM user_backup_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// ConsumeBackupCode marks one unused backup code used, returning false if
// code doesn't match any unused code for userID. Codes are single-use.
func ConsumeBackupCode(ctx context.Context, pool *pgxpool.Pool, userID, code string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE user_backup_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
	`, userID, HashToken(NormalizeBackupCode(code)))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ClearAllTwoFactor removes every 2FA factor, credential, backup code, and
// trusted device for userID and resets the enrollment gate — used by both
// self-service email recovery and admin-assisted reset.
func ClearAllTwoFactor(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, table := range []string{"user_totp", "user_email_2fa", "webauthn_credentials", "user_backup_codes", "user_trusted_devices"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE user_id = $1`, userID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET two_factor_enrolled_at = NULL WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TwoFactorMethods summarizes an account's configured 2FA methods for the
// account settings page.
type TwoFactorMethods struct {
	Enrolled             bool
	TOTPConfirmed        bool
	EmailEnabled         bool
	WebAuthnCredentials  []WebAuthnCredentialInfo
	BackupCodesRemaining int
}

// AvailableTwoFactorMethods lists the second-factor methods userID has
// actually configured, for the login step-up response — a client should
// only offer to prompt for a method the account can complete.
func AvailableTwoFactorMethods(ctx context.Context, pool *pgxpool.Pool, userID string) ([]string, error) {
	m, err := GetTwoFactorMethods(ctx, pool, userID)
	if err != nil {
		return nil, err
	}
	var methods []string
	if m.TOTPConfirmed {
		methods = append(methods, "totp")
	}
	if m.EmailEnabled {
		methods = append(methods, "email")
	}
	if len(m.WebAuthnCredentials) > 0 {
		methods = append(methods, "webauthn")
	}
	return methods, nil
}

// GetTwoFactorMethods aggregates userID's current 2FA configuration.
func GetTwoFactorMethods(ctx context.Context, pool *pgxpool.Pool, userID string) (TwoFactorMethods, error) {
	var m TwoFactorMethods
	var err error
	if m.Enrolled, err = IsTwoFactorEnrolled(ctx, pool, userID); err != nil {
		return m, err
	}
	if _, confirmed, err := GetTOTP(ctx, pool, userID); err == nil {
		m.TOTPConfirmed = confirmed
	} else if !errors.Is(err, ErrNotFound) {
		return m, err
	}
	if m.EmailEnabled, err = IsEmailTwoFactorEnabled(ctx, pool, userID); err != nil {
		return m, err
	}
	if m.WebAuthnCredentials, err = ListWebAuthnCredentials(ctx, pool, userID); err != nil {
		return m, err
	}
	if m.BackupCodesRemaining, err = CountUnusedBackupCodes(ctx, pool, userID); err != nil {
		return m, err
	}
	return m, nil
}
