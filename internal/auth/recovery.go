package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecoveryTokenTTL is how long a "lost my device" recovery link stays
// valid.
const RecoveryTokenTTL = 30 * time.Minute

// CreateRecoveryToken issues a single-use 2FA recovery token for userID and
// returns the raw value to embed in the emailed link.
func CreateRecoveryToken(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO user_2fa_recovery_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, HashToken(token), time.Now().Add(RecoveryTokenTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeRecoveryToken validates and marks used a recovery token, returning
// the user ID it belongs to. Returns ErrNotFound if the token is unknown,
// expired, or already used.
func ConsumeRecoveryToken(ctx context.Context, pool *pgxpool.Pool, rawToken string) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `
		UPDATE user_2fa_recovery_tokens SET used_at = now()
		WHERE token_hash = $1 AND expires_at > now() AND used_at IS NULL
		RETURNING user_id
	`, HashToken(rawToken)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, err
}
