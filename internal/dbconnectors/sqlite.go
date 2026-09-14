package dbconnectors

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (pure Go, no cgo)
)

type sqliteConnector struct{}

// Open connects to the SQLite file at p.Database (a runtime-local path, not
// a network address — Host/Port/Username/Password/SSLMode/InstanceName/
// SearchPath don't apply to this engine).
//
// ponytail: modernc.org/sqlite is pure Go with no SQLCipher support, so an
// encrypted/passphrase-protected SQLite file isn't supported yet — add a
// cgo-based driver (e.g. mattn/go-sqlite3 built with the sqlcipher tag) if a
// real need for that shows up; it's a deliberate build-complexity tradeoff,
// not an oversight.
func (sqliteConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	dsn := p.Database
	if p.ExtraParams != "" {
		dsn += "?" + p.ExtraParams
	}
	return openAndPing(ctx, "sqlite", dsn)
}
