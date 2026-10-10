// Package devices manages paired native-client devices. A device holds an
// ECDSA P-256 public key, belongs to the user the pairing CLI named, and
// authenticates HTTP requests and WebSocket upgrades with a per-request
// signature (ADR-053) — no token is ever stored or transmitted.
package devices

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching device or code.
var ErrNotFound = errors.New("not found")

// ErrUsed / ErrExpired distinguish the pairing-code failure modes in tests
// and the API's 400s; both surface as 400 to the client.
var (
	ErrUsed    = errors.New("pairing code already used")
	ErrExpired = errors.New("pairing code expired")
)

// pairingCodeTTL bounds how long a CLI-minted code can sit unpaired.
const pairingCodeTTL = 10 * time.Minute

// Device is one paired native client.
type Device struct {
	ID         string
	UserID     string
	Name       string
	PublicKey  string // base64 X.509 SubjectPublicKeyInfo (ECDSA P-256)
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

const deviceColumns = `id, user_id, name, public_key, created_at, last_used_at, revoked_at`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.PublicKey, &d.CreatedAt, &d.LastUsedAt, &d.RevokedAt)
	if err != nil {
		return Device{}, err
	}
	return d, nil
}

// CreatePairingCode mints a one-time code the named user's device will
// exchange for a device row, and returns the raw code (shown once, printed
// by the CLI next to the QR).
func CreatePairingCode(ctx context.Context, pool *pgxpool.Pool, userID, deviceName string) (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := hex.EncodeToString(raw)
	_, err := pool.Exec(ctx, `
		INSERT INTO device_pairing_codes (code_hash, user_id, device_name, expires_at)
		VALUES ($1, $2, $3, $4)`,
		HashCode(code), userID, deviceName, time.Now().Add(pairingCodeTTL))
	if err != nil {
		return "", err
	}
	return code, nil
}

// HashCode hashes a raw pairing code for storage/lookup.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// ConsumePairingCode validates, single-uses, and resolves a raw code to its
// owning user and the device name the operator chose on the CLI.
func ConsumePairingCode(ctx context.Context, pool *pgxpool.Pool, code string) (userID, deviceName string, err error) {
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var validUntil *time.Time
		var usedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT user_id, device_name, expires_at, used_at
			FROM device_pairing_codes WHERE code_hash = $1 FOR UPDATE`,
			HashCode(code)).Scan(&userID, &deviceName, &validUntil, &usedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if usedAt != nil {
			return ErrUsed
		}
		if time.Now().After(*validUntil) {
			return ErrExpired
		}
		_, err = tx.Exec(ctx, `UPDATE device_pairing_codes SET used_at = now() WHERE code_hash = $1`, HashCode(code))
		return err
	})
	return userID, deviceName, err
}

// CreateDevice registers a paired device (the public key is only sent once,
// at pairing).
func CreateDevice(ctx context.Context, pool *pgxpool.Pool, userID, name, publicKey string) (Device, error) {
	return scanDevice(pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, public_key)
		VALUES ($1, $2, $3)
		RETURNING `+deviceColumns, userID, name, publicKey))
}

// Get returns one device by ID.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Device, error) {
	d, err := scanDevice(pool.QueryRow(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// ListForUser returns the user's devices, newest first.
func ListForUser(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Device, error) {
	rows, err := pool.Query(ctx, `SELECT `+deviceColumns+` FROM devices WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Revoke marks a device revoked so its next signed request 401s. Returns
// whether a live (unrevoked) device matched.
func Revoke(ctx context.Context, pool *pgxpool.Pool, deviceID string) (bool, error) {
	tag, err := pool.Exec(ctx,
		`UPDATE devices SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, deviceID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Touch stamps last_used_at — called on each successful signature check.
func Touch(ctx context.Context, pool *pgxpool.Pool, deviceID string) {
	_, _ = pool.Exec(ctx, `UPDATE devices SET last_used_at = now() WHERE id = $1`, deviceID)
}

// ValidatePublicKey checks that pkB64 is the base64 SPKI of an ECDSA P-256
// key — the pairing endpoint refuses anything else.
func ValidatePublicKey(pkB64 string) error {
	der, err := base64.StdEncoding.DecodeString(pkB64)
	if err != nil {
		return fmt.Errorf("public key must be base64: %w", err)
	}
	anyKey, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("public key must be X.509 SPKI: %w", err)
	}
	key, ok := anyKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return fmt.Errorf("public key must be ECDSA P-256")
	}
	return nil
}
