package dbconnectors

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

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

// quoteSQLiteIdent double-quotes name (standard SQL identifier quoting,
// SQLite understands it) for the few places a table/index name has to be
// interpolated directly into a PRAGMA statement — PRAGMA doesn't accept
// bind parameters. Embedded quotes are escaped by doubling, matching the
// standard SQL rule, so this is safe against a maliciously-named table.
func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Schemas returns nil — SQLite has no schema concept; the UI skips the
// schema level entirely for this engine.
func (sqliteConnector) Schemas(ctx context.Context, db *sql.DB, p Params) ([]string, error) {
	return nil, nil
}

func (sqliteConnector) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name, type FROM sqlite_master
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTables(rows)
}

func (sqliteConnector) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteSQLiteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out = append(out, Column{Name: name, Type: ctype, Nullable: notnull == 0, Default: dflt.String, Position: cid + 1})
	}
	return out, rows.Err()
}

// sqliteIndexColumns runs PRAGMA index_info(name) and returns its columns,
// in index order. Shared by Indexes and Constraints (unique constraints
// show up as indexes with origin='u').
func sqliteIndexColumns(ctx context.Context, db *sql.DB, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA index_info(`+quoteSQLiteIdent(name)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var cname string
		if err := rows.Scan(&seqno, &cid, &cname); err != nil {
			return nil, err
		}
		cols = append(cols, cname)
	}
	return cols, rows.Err()
}

func (sqliteConnector) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA index_list(`+quoteSQLiteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	type meta struct {
		name, origin string
		unique       int
	}
	var metas []meta
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		metas = append(metas, meta{name: name, origin: origin, unique: unique})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Index, 0, len(metas))
	for _, m := range metas {
		cols, err := sqliteIndexColumns(ctx, db, m.name)
		if err != nil {
			return nil, err
		}
		out = append(out, Index{Name: m.name, Columns: cols, Unique: m.unique == 1, Primary: m.origin == "pk"})
	}
	return out, nil
}

func (sqliteConnector) Constraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	var out []Constraint

	// Primary key: table_info's pk column is the column's 1-based position
	// within the primary key (0 = not part of it).
	pkRows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteSQLiteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	type pkcol struct {
		name string
		pk   int
	}
	var pks []pkcol
	for pkRows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := pkRows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			pkRows.Close()
			return nil, err
		}
		if pk > 0 {
			pks = append(pks, pkcol{name, pk})
		}
	}
	pkRows.Close()
	if len(pks) > 0 {
		sort.Slice(pks, func(i, j int) bool { return pks[i].pk < pks[j].pk })
		names := make([]string, len(pks))
		for i, p := range pks {
			names[i] = p.name
		}
		out = append(out, Constraint{Name: table + "_pk", Type: "primary_key", Columns: names})
	}

	// Foreign keys, grouped by id (one row per referencing column).
	fkRows, err := db.QueryContext(ctx, `PRAGMA foreign_key_list(`+quoteSQLiteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	var order []int
	byID := map[int]*Constraint{}
	for fkRows.Next() {
		var id, seq int
		var reftable, from, to, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &reftable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			fkRows.Close()
			return nil, err
		}
		c, ok := byID[id]
		if !ok {
			c = &Constraint{Name: fmt.Sprintf("%s_fk%d", table, id), Type: "foreign_key", References: reftable + "(" + to + ")"}
			byID[id] = c
			order = append(order, id)
		}
		c.Columns = append(c.Columns, from)
	}
	fkRows.Close()
	if err := fkRows.Err(); err != nil {
		return nil, err
	}
	for _, id := range order {
		out = append(out, *byID[id])
	}

	// Unique constraints show up as indexes with origin='u' (as opposed to
	// 'c' for a plain CREATE INDEX, or 'pk' already handled above).
	ixRows, err := db.QueryContext(ctx, `PRAGMA index_list(`+quoteSQLiteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	var uniqueIndexes []string
	for ixRows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := ixRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			ixRows.Close()
			return nil, err
		}
		if origin == "u" {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	ixRows.Close()
	if err := ixRows.Err(); err != nil {
		return nil, err
	}
	for _, name := range uniqueIndexes {
		cols, err := sqliteIndexColumns(ctx, db, name)
		if err != nil {
			return nil, err
		}
		out = append(out, Constraint{Name: name, Type: "unique", Columns: cols})
	}

	return out, nil
}

// Triggers returns each trigger's name only — SQLite doesn't expose
// event/timing as structured metadata short of parsing the trigger's SQL
// text, which is out of scope here (represented honestly: Event/Timing
// stay empty rather than guessed).
func (sqliteConnector) Triggers(ctx context.Context, db *sql.DB, schema, table string) ([]Trigger, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = ? ORDER BY name`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		var t Trigger
		if err := rows.Scan(&t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
