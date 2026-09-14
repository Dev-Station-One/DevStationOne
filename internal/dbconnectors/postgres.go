package dbconnectors

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

func pgQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func pgPlaceholder(n int) string { return fmt.Sprintf("$%d", n) }

type postgresConnector struct{}

func (postgresConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	u := url.URL{
		Scheme: "postgres",
		Host:   hostPort(p.Host, p.Port, 5432),
		Path:   "/" + p.Database,
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	q := url.Values{}
	if p.SSLMode != "" {
		q.Set("sslmode", p.SSLMode)
	}
	if p.SearchPath != "" {
		q.Set("search_path", p.SearchPath)
	}
	setExtraParams(q, p.ExtraParams)
	u.RawQuery = q.Encode()

	return openAndPing(ctx, "pgx", u.String())
}

func (postgresConnector) Schemas(ctx context.Context, db *sql.DB, p Params) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE nspname NOT IN ('pg_catalog', 'information_schema')
		  AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp%'
		ORDER BY nspname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (postgresConnector) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
		FROM information_schema.tables WHERE table_schema = $1
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTables(rows)
}

func (postgresConnector) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable = 'YES', COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanColumns(rows)
}

func (postgresConnector) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.relname, a.attname, ix.indisunique, ix.indisprimary
		FROM pg_class t
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_index ix ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND t.relname = $2
		ORDER BY i.relname, k.ord`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupIndexes(rows)
}

func (postgresConnector) Constraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tc.constraint_name, tc.constraint_type, kcu.column_name,
		       ccu.table_schema, ccu.table_name, ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		LEFT JOIN information_schema.constraint_column_usage ccu
		  ON tc.constraint_name = ccu.constraint_name AND tc.constraint_type = 'FOREIGN KEY'
		     AND tc.table_schema = ccu.table_schema
		WHERE tc.table_schema = $1 AND tc.table_name = $2
		ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupConstraints(rows)
}

func (postgresConnector) Triggers(ctx context.Context, db *sql.DB, schema, table string) ([]Trigger, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT trigger_name, event_manipulation, action_timing
		FROM information_schema.triggers
		WHERE event_object_schema = $1 AND event_object_table = $2
		ORDER BY trigger_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriggers(rows)
}

func (postgresConnector) Rows(ctx context.Context, db *sql.DB, schema, table string, limit, offset int, sort string, sortDesc bool, filter string) (RowsResult, error) {
	q := "SELECT * FROM " + pgQuoteIdent(schema) + "." + pgQuoteIdent(table)
	if filter != "" {
		q += " WHERE " + filter
	}
	if sort != "" {
		q += " ORDER BY " + pgQuoteIdent(sort)
		if sortDesc {
			q += " DESC"
		}
	}
	q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit+1, offset)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return RowsResult{}, err
	}
	defer rows.Close()
	return scanRows(rows, limit)
}

func (postgresConnector) Commit(ctx context.Context, db *sql.DB, schema, table string, edits []RowEdit) error {
	return buildAndExecCommit(ctx, db, pgQuoteIdent(schema)+"."+pgQuoteIdent(table), edits, pgQuoteIdent, pgPlaceholder)
}

func (postgresConnector) Query(ctx context.Context, db *sql.DB, sqlText string, limit int) (RowsResult, error) {
	return genericQuery(ctx, db, sqlText, limit)
}
