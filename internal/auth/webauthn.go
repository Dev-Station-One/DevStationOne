package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

// NewWebAuthn builds the relying-party handle used for every passkey
// ceremony. rpID/rpOrigin must match the origin the browser actually sees
// (docs/DECISIONS.md ADR-047) or every ceremony fails closed. Empty values
// default to localhost so a zero-value config (as in tests that never
// exercise WebAuthn) still constructs a valid handle instead of erroring.
func NewWebAuthn(rpID, rpOrigin string) (*webauthnlib.WebAuthn, error) {
	if rpID == "" {
		rpID = "localhost"
	}
	if rpOrigin == "" {
		rpOrigin = "http://localhost:8080"
	}
	return webauthnlib.New(&webauthnlib.Config{
		RPDisplayName: "DevStationOne",
		RPID:          rpID,
		RPOrigins:     []string{rpOrigin},
	})
}

// WebAuthnCredentialInfo is one registered credential as shown to the
// account settings page — never includes the public key material.
type WebAuthnCredentialInfo struct {
	ID          string
	Name        string
	ResidentKey bool
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// WebAuthnUser adapts auth.User to the webauthnlib.User interface required
// by every Begin/Finish ceremony call. Exported (with an exported User
// field) so the api package can recover the underlying account from the
// webauthnlib.User a passwordless login ceremony hands back.
type WebAuthnUser struct {
	User        User
	Credentials []webauthnlib.Credential
}

func (u WebAuthnUser) WebAuthnID() []byte   { return []byte(u.User.ID) }
func (u WebAuthnUser) WebAuthnName() string { return u.User.Email }
func (u WebAuthnUser) WebAuthnDisplayName() string {
	if u.User.DisplayName != "" {
		return u.User.DisplayName
	}
	return u.User.Email
}
func (u WebAuthnUser) WebAuthnCredentials() []webauthnlib.Credential { return u.Credentials }

// LoadWebAuthnUser builds the webauthnlib.User for a registration or
// 2FA-login ceremony belonging to an already-authenticated user.
func LoadWebAuthnUser(ctx context.Context, pool *pgxpool.Pool, user User) (webauthnlib.User, error) {
	rows, err := listWebAuthnCredentialRows(ctx, pool, user.ID)
	if err != nil {
		return nil, err
	}
	creds := make([]webauthnlib.Credential, len(rows))
	for i, r := range rows {
		creds[i] = r.credential
	}
	return WebAuthnUser{User: user, Credentials: creds}, nil
}

// LoadWebAuthnUserByHandle resolves the account behind a discoverable
// (passwordless) credential's userHandle — the DiscoverableUserHandler
// FinishPasskeyLogin requires. rawID is unused: the handle alone identifies
// the account, matching the library's own example usage.
func LoadWebAuthnUserByHandle(ctx context.Context, pool *pgxpool.Pool) func(rawID, userHandle []byte) (webauthnlib.User, error) {
	return func(rawID, userHandle []byte) (webauthnlib.User, error) {
		u, err := GetUserByID(ctx, pool, string(userHandle))
		if err != nil {
			return nil, err
		}
		return LoadWebAuthnUser(ctx, pool, u)
	}
}

type webauthnCredentialRow struct {
	id          string
	name        string
	residentKey bool
	credential  webauthnlib.Credential
	createdAt   time.Time
	lastUsedAt  *time.Time
}

func listWebAuthnCredentialRows(ctx context.Context, pool *pgxpool.Pool, userID string) ([]webauthnCredentialRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, resident_key, credential, created_at, last_used_at
		FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []webauthnCredentialRow
	for rows.Next() {
		var r webauthnCredentialRow
		var credJSON []byte
		if err := rows.Scan(&r.id, &r.name, &r.residentKey, &credJSON, &r.createdAt, &r.lastUsedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(credJSON, &r.credential); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListWebAuthnCredentials returns userID's registered passkeys for display
// (account settings page).
func ListWebAuthnCredentials(ctx context.Context, pool *pgxpool.Pool, userID string) ([]WebAuthnCredentialInfo, error) {
	rows, err := listWebAuthnCredentialRows(ctx, pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]WebAuthnCredentialInfo, len(rows))
	for i, r := range rows {
		out[i] = WebAuthnCredentialInfo{ID: r.id, Name: r.name, ResidentKey: r.residentKey, CreatedAt: r.createdAt, LastUsedAt: r.lastUsedAt}
	}
	return out, nil
}

// StoreWebAuthnCredential persists a newly registered credential, completing
// enrollment if it's the account's first confirmed factor.
func StoreWebAuthnCredential(ctx context.Context, pool *pgxpool.Pool, userID, name string, residentKey bool, cred *webauthnlib.Credential) error {
	credJSON, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO webauthn_credentials (user_id, credential_id, name, resident_key, credential)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, []byte(cred.ID), name, residentKey, credJSON); err != nil {
		return err
	}
	return markEnrolledIfFirst(ctx, pool, userID)
}

// TouchWebAuthnCredential persists the updated sign counter (clone
// detection) and last-used timestamp after a successful assertion.
func TouchWebAuthnCredential(ctx context.Context, pool *pgxpool.Pool, cred *webauthnlib.Credential) error {
	credJSON, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE webauthn_credentials SET credential = $2, last_used_at = now()
		WHERE credential_id = $1
	`, []byte(cred.ID), credJSON)
	return err
}

// DeleteWebAuthnCredential removes one of userID's credentials by its row
// ID, refusing (ErrLastFactor) if the account is enrolled and this is its
// only remaining factor.
func DeleteWebAuthnCredential(ctx context.Context, pool *pgxpool.Pool, userID, credentialRowID string) error {
	return removeFactor(ctx, pool, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`, credentialRowID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
