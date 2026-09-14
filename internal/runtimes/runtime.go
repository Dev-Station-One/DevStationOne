// Package runtimes manages Development Runtime registration, credentials,
// and connection state on the Control Server.
package runtimes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/protocol"
)

// ErrNotFound is returned when a lookup finds no matching runtime.
var ErrNotFound = errors.New("not found")

// Status values a Runtime row can hold.
const (
	StatusPending = "pending"
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusRevoked = "revoked"
)

// Runtime is a registered Development Runtime.
type Runtime struct {
	ID             string
	Name           string
	TokenHash      string
	Hostname       string
	OS             string
	Arch           string
	RuntimeVersion string
	Capabilities   protocol.Capabilities
	Status         string
	CreatedBy      string
	EnrolledAt     *time.Time
	LastSeenAt     *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
}

// Create registers a new runtime and returns it along with the raw
// enrollment token, which is shown to the caller exactly once.
func Create(ctx context.Context, pool *pgxpool.Pool, name, createdByUserID string) (Runtime, string, error) {
	token, err := auth.NewSessionToken()
	if err != nil {
		return Runtime{}, "", err
	}

	var rt Runtime
	err = pool.QueryRow(ctx, `
		INSERT INTO runtimes (name, token_hash, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, name, token_hash, hostname, os, arch, runtime_version,
			capabilities, status, created_by, enrolled_at, last_seen_at, revoked_at, created_at
	`, name, auth.HashToken(token), createdByUserID).Scan(
		&rt.ID, &rt.Name, &rt.TokenHash, &rt.Hostname, &rt.OS, &rt.Arch, &rt.RuntimeVersion,
		capabilitiesScanner(&rt.Capabilities), &rt.Status, &rt.CreatedBy, &rt.EnrolledAt, &rt.LastSeenAt, &rt.RevokedAt, &rt.CreatedAt,
	)
	if err != nil {
		return Runtime{}, "", err
	}
	return rt, token, nil
}

// List returns all registered runtimes, most recently created first.
func List(ctx context.Context, pool *pgxpool.Pool) ([]Runtime, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, token_hash, hostname, os, arch, runtime_version,
			capabilities, status, created_by, enrolled_at, last_seen_at, revoked_at, created_at
		FROM runtimes
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Runtime
	for rows.Next() {
		var rt Runtime
		if err := rows.Scan(
			&rt.ID, &rt.Name, &rt.TokenHash, &rt.Hostname, &rt.OS, &rt.Arch, &rt.RuntimeVersion,
			capabilitiesScanner(&rt.Capabilities), &rt.Status, &rt.CreatedBy, &rt.EnrolledAt, &rt.LastSeenAt, &rt.RevokedAt, &rt.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

// GetByTokenHash looks up a runtime by the hash of its enrollment token,
// regardless of status, so the caller can decide how to treat pending,
// online, offline, or revoked runtimes.
func GetByTokenHash(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (Runtime, error) {
	var rt Runtime
	err := pool.QueryRow(ctx, `
		SELECT id, name, token_hash, hostname, os, arch, runtime_version,
			capabilities, status, created_by, enrolled_at, last_seen_at, revoked_at, created_at
		FROM runtimes
		WHERE token_hash = $1
	`, tokenHash).Scan(
		&rt.ID, &rt.Name, &rt.TokenHash, &rt.Hostname, &rt.OS, &rt.Arch, &rt.RuntimeVersion,
		capabilitiesScanner(&rt.Capabilities), &rt.Status, &rt.CreatedBy, &rt.EnrolledAt, &rt.LastSeenAt, &rt.RevokedAt, &rt.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, ErrNotFound
	}
	if err != nil {
		return Runtime{}, err
	}
	return rt, nil
}

// GetByID looks up a runtime by its ID.
func GetByID(ctx context.Context, pool *pgxpool.Pool, id string) (Runtime, error) {
	var rt Runtime
	err := pool.QueryRow(ctx, `
		SELECT id, name, token_hash, hostname, os, arch, runtime_version,
			capabilities, status, created_by, enrolled_at, last_seen_at, revoked_at, created_at
		FROM runtimes
		WHERE id = $1
	`, id).Scan(
		&rt.ID, &rt.Name, &rt.TokenHash, &rt.Hostname, &rt.OS, &rt.Arch, &rt.RuntimeVersion,
		capabilitiesScanner(&rt.Capabilities), &rt.Status, &rt.CreatedBy, &rt.EnrolledAt, &rt.LastSeenAt, &rt.RevokedAt, &rt.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, ErrNotFound
	}
	if err != nil {
		return Runtime{}, err
	}
	return rt, nil
}

// MarkOnline records a runtime's hello payload and transitions it to online.
// enrolled_at is set the first time this happens and never overwritten.
func MarkOnline(ctx context.Context, pool *pgxpool.Pool, id string, hello protocol.HelloPayload) error {
	caps, err := json.Marshal(hello.Capabilities)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE runtimes SET
			hostname = $2,
			os = $3,
			arch = $4,
			runtime_version = $5,
			capabilities = $6::jsonb,
			status = $7,
			enrolled_at = COALESCE(enrolled_at, now()),
			last_seen_at = now()
		WHERE id = $1
	`, id, hello.Hostname, hello.OS, hello.Arch, hello.RuntimeVersion, string(caps), StatusOnline)
	return err
}

// Heartbeat bumps last_seen_at for a connected runtime.
func Heartbeat(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `UPDATE runtimes SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

// MarkOffline transitions a runtime to offline when its connection drops,
// unless it has been revoked in the meantime.
func MarkOffline(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `
		UPDATE runtimes SET status = $2, last_seen_at = now()
		WHERE id = $1 AND status != $3
	`, id, StatusOffline, StatusRevoked)
	return err
}

// Revoke marks a runtime revoked, preventing future connections. It reports
// whether a matching runtime was found.
func Revoke(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE runtimes SET status = $2, revoked_at = now() WHERE id = $1
	`, id, StatusRevoked)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Reactivate clears a runtime's revocation, returning it to pending status
// so it can reconnect using its existing enrollment token — re-enabling
// does not reissue a token. It reports whether a matching runtime was
// found.
func Reactivate(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE runtimes SET status = $2, revoked_at = NULL WHERE id = $1
	`, id, StatusPending)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// capabilitiesScanner adapts a *protocol.Capabilities field for pgx's Scan,
// unmarshaling the jsonb column into it.
func capabilitiesScanner(dst *protocol.Capabilities) any {
	return &jsonScanner{dst: dst}
}

type jsonScanner struct {
	dst *protocol.Capabilities
}

func (s *jsonScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("unsupported capabilities scan type")
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, s.dst)
}
