package auth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrustedDeviceCookieName is the browser cookie that, when it matches an
// unexpired user_trusted_devices row, skips the second factor on login
// (password/passkey is still always required).
const TrustedDeviceCookieName = "devstationone_2fa_trust"

// TrustedDeviceTTL is how long a trusted-device cookie remains valid.
const TrustedDeviceTTL = 30 * 24 * time.Hour

// TrustedDevice is one browser a user has chosen to trust.
type TrustedDevice struct {
	ID        string
	UserAgent string
	IPAddress string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateTrustedDevice stores a new trusted device for userID and returns the
// raw token to set as a cookie.
func CreateTrustedDevice(ctx context.Context, pool *pgxpool.Pool, userID, userAgent, ipAddress string) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO user_trusted_devices (user_id, token_hash, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, HashToken(token), userAgent, ipAddress, time.Now().Add(TrustedDeviceTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// IsTrustedDevice reports whether rawToken matches an unexpired trusted
// device belonging to userID.
func IsTrustedDevice(ctx context.Context, pool *pgxpool.Pool, userID, rawToken string) (bool, error) {
	var trusted bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_trusted_devices
			WHERE user_id = $1 AND token_hash = $2 AND expires_at > now()
		)
	`, userID, HashToken(rawToken)).Scan(&trusted)
	return trusted, err
}

// ListTrustedDevices returns userID's unexpired trusted devices, most
// recently created first.
func ListTrustedDevices(ctx context.Context, pool *pgxpool.Pool, userID string) ([]TrustedDevice, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, user_agent, ip_address, created_at, expires_at
		FROM user_trusted_devices WHERE user_id = $1 AND expires_at > now()
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrustedDevice
	for rows.Next() {
		var d TrustedDevice
		if err := rows.Scan(&d.ID, &d.UserAgent, &d.IPAddress, &d.CreatedAt, &d.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteTrustedDevice revokes one of userID's trusted devices, scoped so one
// user cannot revoke another user's device.
func DeleteTrustedDevice(ctx context.Context, pool *pgxpool.Pool, userID, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM user_trusted_devices WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
