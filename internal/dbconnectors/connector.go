// Package dbconnectors opens and tests connections to the database engines a
// Project can browse (PRD §6): PostgreSQL, MySQL/MariaDB, Microsoft SQL
// Server, and SQLite. Runs only on the Development Runtime — the Control
// Server never opens a database connection itself (docs/SECURITY.md:
// "Execute database operations on the runtime").
package dbconnectors

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
)

// Params identifies and authenticates one database connection. Built by the
// runtime from a protocol.DBConnectionParams it received over the control
// connection — see internal/agent/db_manager.go.
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

// Connector opens a verified connection for one database engine.
type Connector interface {
	// Open opens a connection pool for p and verifies it with a ping. The
	// caller must Close the result.
	Open(ctx context.Context, p Params) (*sql.DB, error)
}

// SupportedEngines lists every engine name this build can connect to,
// suitable for hello.Capabilities.Databases. All four drivers are pure Go
// and always compiled in, so — unlike systemd --user — availability doesn't
// depend on anything else installed on the runtime host.
func SupportedEngines() []string {
	return []string{"postgresql", "mysql", "mssql", "sqlite"}
}

// For returns the Connector for engine, or an error if engine isn't one of
// SupportedEngines.
func For(engine string) (Connector, error) {
	switch engine {
	case "postgresql":
		return postgresConnector{}, nil
	case "mysql":
		return mysqlConnector{}, nil
	case "mssql":
		return mssqlConnector{}, nil
	case "sqlite":
		return sqliteConnector{}, nil
	default:
		return nil, fmt.Errorf("unsupported database engine %q", engine)
	}
}

// openAndPing opens db via database/sql and verifies it with a bounded ping,
// closing it and returning an error on failure so callers never leak a
// half-open pool.
func openAndPing(ctx context.Context, driverName, dsn string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// hostPort formats host:port, filling in def when port is unset (0).
func hostPort(host string, port, def int) string {
	if port == 0 {
		port = def
	}
	return host + ":" + strconv.Itoa(port)
}

// parseExtraParams parses a "key=value&key2=value2" free-form connection
// string (Connection.ExtraParams — an escape hatch for anything the named
// fields don't cover).
func parseExtraParams(extra string) (url.Values, error) {
	if extra == "" {
		return url.Values{}, nil
	}
	return url.ParseQuery(extra)
}

// setExtraParams merges extra into q, ignoring an unparseable value rather
// than failing the whole connection over it.
func setExtraParams(q url.Values, extra string) {
	extraValues, err := parseExtraParams(extra)
	if err != nil {
		return
	}
	for k, vs := range extraValues {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
}
