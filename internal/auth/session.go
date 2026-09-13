package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CookieName is the browser session cookie name.
const CookieName = "devstation_session"

// SessionTTL is how long a session stays valid since creation.
const SessionTTL = 7 * 24 * time.Hour

// Session is a browser login session.
type Session struct {
	TokenHash  string
	UserID     string
	UserAgent  string
	IPAddress  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// HashToken returns the storage/lookup hash for a raw session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken generates a high-entropy random session token.
func NewSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreateSession stores a new session for userID and returns the raw token to set as a cookie.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, userID, userAgent, ipAddress string) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, HashToken(token), userID, userAgent, ipAddress, time.Now().Add(SessionTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionAndUser looks up a session by its raw cookie token and the user it belongs to.
// It also bumps last_seen_at. Expired or unknown tokens return ErrNotFound.
func SessionAndUser(ctx context.Context, pool *pgxpool.Pool, token string) (Session, User, error) {
	var s Session
	var u User
	err := pool.QueryRow(ctx, `
		UPDATE sessions SET last_seen_at = now()
		WHERE token_hash = $1 AND expires_at > now()
		RETURNING token_hash, user_id, user_agent, ip_address, created_at, last_seen_at, expires_at
	`, HashToken(token)).Scan(&s.TokenHash, &s.UserID, &s.UserAgent, &s.IPAddress, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, err
	}

	err = pool.QueryRow(ctx, `
		SELECT id, email, is_admin, created_at, terminal_font, theme FROM users WHERE id = $1
	`, s.UserID).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme)
	if err != nil {
		return Session{}, User{}, err
	}
	return s, u, nil
}

// ListSessions returns all non-expired sessions for a user, most recent first.
func ListSessions(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Session, error) {
	rows, err := pool.Query(ctx, `
		SELECT token_hash, user_id, user_agent, ip_address, created_at, last_seen_at, expires_at
		FROM sessions
		WHERE user_id = $1 AND expires_at > now()
		ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.TokenHash, &s.UserID, &s.UserAgent, &s.IPAddress, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// DeleteSessionByToken removes the session identified by its raw cookie token.
func DeleteSessionByToken(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, HashToken(token))
	return err
}

// DeleteSessionForUser removes a session by its hash, scoped to userID so one
// user cannot revoke another user's session.
func DeleteSessionForUser(ctx context.Context, pool *pgxpool.Pool, userID, tokenHash string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1 AND user_id = $2`, tokenHash, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
