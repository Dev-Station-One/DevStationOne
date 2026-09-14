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
	"strings"
	"unicode/utf8"
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

// Table describes one table or view.
type Table struct {
	Name string
	// Kind is "table" or "view".
	Kind string
}

// Column describes one table/view column.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	// Default is the column's default expression, or "" if none.
	Default  string
	Position int
}

// Index describes one table index.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
}

// Constraint describes one table constraint.
type Constraint struct {
	Name string
	// Type is "primary_key", "foreign_key", "unique", or "check".
	Type    string
	Columns []string
	// References is "schema.table(column)" for a foreign_key constraint,
	// otherwise "".
	References string
}

// Trigger describes one table trigger. Event/Timing are best-effort — not
// every engine exposes them as cleanly as a name (SQLite in particular:
// see sqlite.go).
type Trigger struct {
	Name   string
	Event  string
	Timing string
}

// RowEdit is one staged insert/update/delete for Commit.
type RowEdit struct {
	// Op is "insert", "update", or "delete".
	Op string
	// Original identifies the row being updated/deleted — the column
	// values as originally fetched by Rows, used to build a WHERE clause.
	// Unused for insert.
	Original map[string]any
	// Values are the new column values for insert/update. Unused for
	// delete.
	Values map[string]any
}

// RowsResult is the result of Rows or Query. Exactly one of (Columns+Rows)
// or (IsExec+RowsAffected) is meaningful, matching whether the statement
// produced a result set.
type RowsResult struct {
	Columns []string
	// Rows holds each row's values in Columns order. A []byte value has
	// already been normalized to a UTF-8 string where possible (see
	// normalizeValue) — only genuinely non-text data stays []byte, which
	// encoding/json base64-encodes.
	Rows [][]any
	// HasMore is true when more rows exist beyond what was returned
	// (Limit was reached).
	HasMore bool
	// IsExec is true for a statement with no result set (Query's raw SQL
	// only — Rows always produces a result set).
	IsExec       bool
	RowsAffected int64
}

// Connector opens a verified connection for one database engine and
// introspects its schema. schema is "" for an engine with no schema concept
// (SQLite) or a single implicit schema (MySQL/MariaDB — see Schemas).
type Connector interface {
	// Open opens a connection pool for p and verifies it with a ping. The
	// caller must Close the result.
	Open(ctx context.Context, p Params) (*sql.DB, error)

	// Schemas lists the schemas within p's connected database. A schema is
	// always scoped to the one database the connection is already pinned
	// to (Params.Database) — browsing another database on the same server
	// would need a separate connection, which is out of scope here.
	// PostgreSQL/MSSQL return real schemas; MySQL/MariaDB (no separate
	// schema concept — a "database" already is one) returns a single
	// schema named after Params.Database; SQLite returns nil.
	Schemas(ctx context.Context, db *sql.DB, p Params) ([]string, error)
	Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error)
	Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error)
	Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error)
	Constraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error)
	Triggers(ctx context.Context, db *sql.DB, schema, table string) ([]Trigger, error)

	// Rows returns a page of table/view data. sort is a column name (already
	// validated against the table's own columns by the caller) or "";
	// sortDesc reverses it. filter, if non-empty, is a raw SQL boolean
	// expression appended after WHERE — the same deliberate arbitrary-SQL
	// trust model as Query (docs/DECISIONS.md ADR-021), not a parameterized
	// value.
	Rows(ctx context.Context, db *sql.DB, schema, table string, limit, offset int, sort string, sortDesc bool, filter string) (RowsResult, error)

	// Commit applies edits to table as parameterized statements — never by
	// interpolating a row's values into SQL text, unlike filter/Query above.
	Commit(ctx context.Context, db *sql.DB, schema, table string, edits []RowEdit) error

	// Query runs sqlText verbatim — arbitrary SQL, the SQL Editor's
	// intentional parallel to the terminal (ADR-021). limit caps how many
	// result rows are read back for a SELECT-shaped statement; an
	// INSERT/UPDATE/DELETE/DDL statement is run via Exec instead and
	// reports RowsAffected.
	Query(ctx context.Context, db *sql.DB, sqlText string, limit int) (RowsResult, error)
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

// scanStrings collects a single-string-column result set.
func scanStrings(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanTables collects rows shaped (name, kind) into Tables. Shared by every
// information_schema-based engine's Tables query.
func scanTables(rows *sql.Rows) ([]Table, error) {
	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Kind); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// scanColumns collects rows shaped (name, type, nullable, default,
// position) into Columns. Shared by every information_schema-based engine's
// Columns query.
func scanColumns(rows *sql.Rows) ([]Column, error) {
	var out []Column
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.Position); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanTriggers collects rows shaped (name, event, timing) into Triggers.
func scanTriggers(rows *sql.Rows) ([]Trigger, error) {
	var out []Trigger
	for rows.Next() {
		var t Trigger
		if err := rows.Scan(&t.Name, &t.Event, &t.Timing); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// groupIndexes assembles rows shaped (index_name, column_name, unique,
// primary) — one row per column, ordered by index name then column
// position — into one Index per name. Shared by every engine's Indexes
// query, which all produce this same row shape from different SQL.
func groupIndexes(rows *sql.Rows) ([]Index, error) {
	var out []Index
	for rows.Next() {
		var name, column string
		var unique, primary bool
		if err := rows.Scan(&name, &column, &unique, &primary); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Name == name {
			out[n-1].Columns = append(out[n-1].Columns, column)
			continue
		}
		out = append(out, Index{Name: name, Columns: []string{column}, Unique: unique, Primary: primary})
	}
	return out, rows.Err()
}

// groupConstraints assembles rows shaped (constraint_name, constraint_type,
// column_name, foreign_schema, foreign_table, foreign_column) — one row per
// column, ordered by constraint name then column position, foreign_* empty
// unless constraint_type is "foreign_key" — into one Constraint per name.
// Shared by every information_schema-based engine's Constraints query.
func groupConstraints(rows *sql.Rows) ([]Constraint, error) {
	var out []Constraint
	for rows.Next() {
		var name, ctype, column string
		var foreignSchema, foreignTable, foreignColumn sql.NullString
		if err := rows.Scan(&name, &ctype, &column, &foreignSchema, &foreignTable, &foreignColumn); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Name == name {
			out[n-1].Columns = append(out[n-1].Columns, column)
			continue
		}
		c := Constraint{Name: name, Type: constraintTypeName(ctype), Columns: []string{column}}
		if foreignTable.Valid {
			c.References = foreignSchema.String + "." + foreignTable.String + "(" + foreignColumn.String + ")"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// normalizeValue converts a scanned driver value into something
// JSON-friendly: a []byte that's valid UTF-8 becomes a string (most
// TEXT/VARCHAR/DECIMAL columns come back as []byte from at least the MySQL
// driver even when scanned into `any`); genuinely binary []byte is left
// alone, which encoding/json base64-encodes.
func normalizeValue(v any) any {
	b, ok := v.([]byte)
	if !ok {
		return v
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return b
}

// scanRows reads up to limit+1 rows from rows (already-executed), so the
// caller — whose SQL requested limit+1 — can tell whether more rows exist
// without a separate COUNT(*). Used by both Rows (whose SQL bakes in
// limit+1) and Query (whose arbitrary SQL doesn't; the cap is enforced
// purely by stopping iteration early, which works either way).
func scanRows(rows *sql.Rows, limit int) (RowsResult, error) {
	cols, err := rows.Columns()
	if err != nil {
		return RowsResult{}, err
	}
	result := RowsResult{Columns: cols}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		if len(result.Rows) >= limit {
			result.HasMore = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return RowsResult{}, err
		}
		row := make([]any, len(cols))
		for i, v := range values {
			row[i] = normalizeValue(v)
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

// isExecStatement reports whether sqlText looks like a statement with no
// result set — a simple keyword sniff (the same approach general-purpose
// SQL clients use), not a parser. Good enough to route the SQL Editor's
// arbitrary text to Exec vs Query; a false classification just produces an
// error from the wrong call, not a security issue.
func isExecStatement(sqlText string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sqlText))
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "TRUNCATE", "GRANT", "REVOKE", "REPLACE"} {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

// genericQuery implements Connector.Query identically for every engine —
// running arbitrary SQL verbatim needs no engine-specific building.
func genericQuery(ctx context.Context, db *sql.DB, sqlText string, limit int) (RowsResult, error) {
	if isExecStatement(sqlText) {
		res, err := db.ExecContext(ctx, sqlText)
		if err != nil {
			return RowsResult{}, err
		}
		affected, _ := res.RowsAffected() // not every driver reports this; 0 is a reasonable default
		return RowsResult{IsExec: true, RowsAffected: affected}, nil
	}
	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return RowsResult{}, err
	}
	defer rows.Close()
	return scanRows(rows, limit)
}

// buildAndExecCommit applies edits against quotedTable (already fully
// identifier-quoted and schema-qualified by the caller) using quoteIdent
// for column names and placeholder for bound-parameter markers — the only
// two things that differ across engines; the statement shapes themselves
// are identical. Every value is bound, never interpolated — the one
// deliberate exception to that rule in this package is Query's raw SQL
// text, not this function.
func buildAndExecCommit(ctx context.Context, db *sql.DB, quotedTable string, edits []RowEdit, quoteIdent func(string) string, placeholder func(int) string) error {
	for _, e := range edits {
		switch e.Op {
		case "insert":
			cols := make([]string, 0, len(e.Values))
			phs := make([]string, 0, len(e.Values))
			args := make([]any, 0, len(e.Values))
			n := 1
			for col, val := range e.Values {
				cols = append(cols, quoteIdent(col))
				phs = append(phs, placeholder(n))
				args = append(args, val)
				n++
			}
			q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quotedTable, strings.Join(cols, ", "), strings.Join(phs, ", "))
			if _, err := db.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("insert: %w", err)
			}

		case "update":
			if len(e.Original) == 0 {
				return fmt.Errorf("update requires the row's original values to build a WHERE clause")
			}
			var sets, where []string
			var args []any
			n := 1
			for col, val := range e.Values {
				sets = append(sets, quoteIdent(col)+" = "+placeholder(n))
				args = append(args, val)
				n++
			}
			for col, val := range e.Original {
				where = append(where, quoteIdent(col)+" = "+placeholder(n))
				args = append(args, val)
				n++
			}
			q := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quotedTable, strings.Join(sets, ", "), strings.Join(where, " AND "))
			if _, err := db.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("update: %w", err)
			}

		case "delete":
			if len(e.Original) == 0 {
				return fmt.Errorf("delete requires the row's original values to build a WHERE clause")
			}
			var where []string
			var args []any
			n := 1
			for col, val := range e.Original {
				where = append(where, quoteIdent(col)+" = "+placeholder(n))
				args = append(args, val)
				n++
			}
			q := fmt.Sprintf("DELETE FROM %s WHERE %s", quotedTable, strings.Join(where, " AND "))
			if _, err := db.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("delete: %w", err)
			}

		default:
			return fmt.Errorf("unknown row edit op %q", e.Op)
		}
	}
	return nil
}

// constraintTypeName normalizes information_schema.table_constraints'
// constraint_type ("PRIMARY KEY", "FOREIGN KEY", "UNIQUE", "CHECK") to
// Constraint.Type's lower_snake_case values.
func constraintTypeName(t string) string {
	switch t {
	case "PRIMARY KEY":
		return "primary_key"
	case "FOREIGN KEY":
		return "foreign_key"
	case "UNIQUE":
		return "unique"
	case "CHECK":
		return "check"
	default:
		return strings.ToLower(strings.ReplaceAll(t, " ", "_"))
	}
}
