package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const totpIssuer = "DevStationOne"

// ErrInvalidCode is returned when a submitted TOTP/backup/email code fails
// verification.
var ErrInvalidCode = errors.New("invalid code")

// GenerateTOTPKey creates a new TOTP secret for accountEmail. The caller
// seals key.Secret() for storage and shows key.URL() to the user as a QR
// code; nothing is enrolled until ConfirmTOTP validates a code against it.
func GenerateTOTPKey(accountEmail string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: accountEmail})
}

// ValidateTOTPCode checks code against secret allowing +/-1 time step
// (~90s total) of authenticator clock drift.
func ValidateTOTPCode(secret, code string) bool {
	_, ok := matchTOTPStep(secret, code, time.Now())
	return ok
}

// matchTOTPStep returns the 30-second time step (unix/30) within +/-1 of now
// that code is valid for, so login can record it and refuse a replay.
func matchTOTPStep(secret, code string, now time.Time) (int64, bool) {
	cur := now.Unix() / 30
	for s := cur + 1; s >= cur-1; s-- {
		ok, err := totp.ValidateCustom(code, secret, time.Unix(s*30, 0), totp.ValidateOpts{
			Period:    30,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return s, true
		}
	}
	return 0, false
}

// sealTOTPSecret encrypts secret with AES-256-GCM under key
// (DEVSTATIONONE_DB_ENCRYPTION_KEY) — the same scheme
// internal/dbconnections uses for stored database credentials, duplicated
// here rather than exported cross-package for one caller.
func sealTOTPSecret(key []byte, secret string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(secret), nil), nil
}

// openTOTPSecret decrypts a value produced by sealTOTPSecret.
func openTOTPSecret(key, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// SetupTOTPForUser generates a fresh TOTP secret for userID, seals it with
// key, stores it unconfirmed, and returns the otpauth:// URI to render as a
// QR code. The plaintext secret never leaves this function.
func SetupTOTPForUser(ctx context.Context, pool *pgxpool.Pool, userID, accountEmail string, key []byte) (string, error) {
	otpKey, err := GenerateTOTPKey(accountEmail)
	if err != nil {
		return "", err
	}
	sealed, err := sealTOTPSecret(key, otpKey.Secret())
	if err != nil {
		return "", err
	}
	if err := SetupTOTP(ctx, pool, userID, sealed); err != nil {
		return "", err
	}
	return otpKey.URL(), nil
}

// ConfirmTOTPForUser validates code against userID's pending TOTP setup and,
// if it matches, confirms the factor (completing enrollment if it's the
// account's first confirmed factor). Returns ErrInvalidCode on a mismatch.
func ConfirmTOTPForUser(ctx context.Context, pool *pgxpool.Pool, userID string, key []byte, code string) error {
	sealed, _, err := GetTOTP(ctx, pool, userID)
	if err != nil {
		return err
	}
	secret, err := openTOTPSecret(key, sealed)
	if err != nil {
		return err
	}
	if !ValidateTOTPCode(secret, code) {
		return ErrInvalidCode
	}
	return ConfirmTOTP(ctx, pool, userID)
}

// VerifyTOTPForUser validates code against userID's confirmed TOTP secret —
// the login step-up path, as opposed to the one-time setup confirmation.
// Returns (false, nil), not an error, if TOTP isn't confirmed for userID.
func VerifyTOTPForUser(ctx context.Context, pool *pgxpool.Pool, userID string, key []byte, code string) (bool, error) {
	sealed, confirmed, err := GetTOTP(ctx, pool, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !confirmed {
		return false, nil
	}
	secret, err := openTOTPSecret(key, sealed)
	if err != nil {
		return false, err
	}
	step, ok := matchTOTPStep(secret, code, time.Now())
	if !ok {
		return false, nil
	}
	// A step is accepted once: the conditional update fails for a code whose
	// step (or a later one) was already used, including a concurrent replay.
	tag, err := pool.Exec(ctx, `UPDATE user_totp SET last_used_step = $2 WHERE user_id = $1 AND last_used_step < $2`, userID, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
