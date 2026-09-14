// Package dbconnections manages stored database-connection records for a
// Project's Database Manager (PRD §6): which engine, host, and (encrypted)
// credentials a saved connection uses. The Control Server never opens these
// connections itself — see internal/dbconnectors, which runs on the
// Development Runtime.
package dbconnections

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching connection.
var ErrNotFound = errors.New("not found")

// Connection is a stored database connection, credentials excluded — see
// Params for the decrypted form used to actually connect.
type Connection struct {
	ID           string
	ProjectID    string
	Name         string
	Engine       string
	Host         string
	Port         int
	Username     string
	Database     string
	SSLMode      string
	InstanceName string
	SearchPath   string
	HasSecret    bool
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Fields groups the non-credential, user-supplied properties of a
// Connection, used for both creation and updates.
type Fields struct {
	Name         string
	Engine       string
	Host         string
	Port         int
	Username     string
	Database     string
	SSLMode      string
	InstanceName string
	SearchPath   string
}

// SecretUpdate carries a new password/extra-params pair. Passing nil to
// Update leaves the stored secret untouched — the "blank password means
// keep the existing one" behavior the edit form relies on.
type SecretUpdate struct {
	Password    string
	ExtraParams string
}

// Params is the decrypted form of a Connection, ready to send to a runtime
// as protocol.DBConnectionParams. Only ResolveParams produces one; it must
// never be logged or returned from an API handler (docs/SECURITY.md).
type Params struct {
	Engine       string
	Host         string
	Port         int
	Username     string
	Password     string
	Database     string
	SSLMode      string
	InstanceName string
	SearchPath   string
	ExtraParams  string
}

const connectionColumns = `id, project_id, name, engine, host, port, username, database_name, ssl_mode, instance_name, search_path, (encrypted_secret IS NOT NULL), created_by, created_at, updated_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Engine, &c.Host, &c.Port, &c.Username, &c.Database,
		&c.SSLMode, &c.InstanceName, &c.SearchPath, &c.HasSecret, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// Create inserts a new connection owned by createdByUserID, sealing secret
// under key.
func Create(ctx context.Context, pool *pgxpool.Pool, projectID string, f Fields, secret SecretUpdate, key []byte, createdByUserID string) (Connection, error) {
	sealed, err := sealSecret(key, secret.Password, secret.ExtraParams)
	if err != nil {
		return Connection{}, err
	}
	c, err := scanConnection(pool.QueryRow(ctx, `
		INSERT INTO database_connections (project_id, name, engine, host, port, username, database_name, ssl_mode, instance_name, search_path, encrypted_secret, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+connectionColumns,
		projectID, f.Name, f.Engine, f.Host, f.Port, f.Username, f.Database, f.SSLMode, f.InstanceName, f.SearchPath, sealed, createdByUserID,
	))
	if err != nil {
		return Connection{}, err
	}
	return c, nil
}

// ListForProject returns every connection on projectID, most recently
// created first.
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]Connection, error) {
	rows, err := pool.Query(ctx, `SELECT `+connectionColumns+` FROM database_connections WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get looks up a connection by ID regardless of project. Callers are
// responsible for authorizing access to the result.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Connection, error) {
	c, err := scanConnection(pool.QueryRow(ctx, `SELECT `+connectionColumns+` FROM database_connections WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	return c, nil
}

// Update overwrites a connection's non-credential fields, and its secret
// only when secret is non-nil.
func Update(ctx context.Context, pool *pgxpool.Pool, id string, f Fields, secret *SecretUpdate, key []byte) (Connection, error) {
	var (
		row pgx.Row
		err error
	)
	if secret == nil {
		row = pool.QueryRow(ctx, `
			UPDATE database_connections SET
				name = $2, engine = $3, host = $4, port = $5, username = $6,
				database_name = $7, ssl_mode = $8, instance_name = $9, search_path = $10,
				updated_at = now()
			WHERE id = $1
			RETURNING `+connectionColumns,
			id, f.Name, f.Engine, f.Host, f.Port, f.Username, f.Database, f.SSLMode, f.InstanceName, f.SearchPath,
		)
	} else {
		var sealed []byte
		sealed, err = sealSecret(key, secret.Password, secret.ExtraParams)
		if err != nil {
			return Connection{}, err
		}
		row = pool.QueryRow(ctx, `
			UPDATE database_connections SET
				name = $2, engine = $3, host = $4, port = $5, username = $6,
				database_name = $7, ssl_mode = $8, instance_name = $9, search_path = $10,
				encrypted_secret = $11, updated_at = now()
			WHERE id = $1
			RETURNING `+connectionColumns,
			id, f.Name, f.Engine, f.Host, f.Port, f.Username, f.Database, f.SSLMode, f.InstanceName, f.SearchPath, sealed,
		)
	}
	c, err := scanConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	return c, nil
}

// Delete removes a connection and reports whether a matching row was found.
func Delete(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM database_connections WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ResolveParams looks up id and decrypts its stored secret under key,
// producing the Params needed to actually dispatch a db.command to a
// runtime. Callers must never log or serialize the result.
func ResolveParams(ctx context.Context, pool *pgxpool.Pool, id string, key []byte) (Params, error) {
	var (
		p      Params
		sealed []byte
	)
	err := pool.QueryRow(ctx, `
		SELECT engine, host, port, username, database_name, ssl_mode, instance_name, search_path, encrypted_secret
		FROM database_connections WHERE id = $1`, id,
	).Scan(&p.Engine, &p.Host, &p.Port, &p.Username, &p.Database, &p.SSLMode, &p.InstanceName, &p.SearchPath, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Params{}, ErrNotFound
	}
	if err != nil {
		return Params{}, err
	}
	if len(sealed) > 0 {
		s, err := openSecret(key, sealed)
		if err != nil {
			return Params{}, err
		}
		p.Password, p.ExtraParams = s.Password, s.ExtraParams
	}
	return p, nil
}
