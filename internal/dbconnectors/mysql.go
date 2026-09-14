package dbconnectors

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
)

func mysqlQuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

type mysqlConnector struct{}

func (mysqlConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = p.Username
	cfg.Passwd = p.Password
	cfg.Net = "tcp"
	cfg.Addr = hostPort(p.Host, p.Port, 3306)
	cfg.DBName = p.Database
	// One of "true", "false", "skip-verify", "preferred", or a name
	// registered via mysql.RegisterTLSConfig — passed straight through, so
	// the connection form's SSL-mode field must use one of those values.
	if p.SSLMode != "" {
		cfg.TLSConfig = p.SSLMode
	}
	cfg.Params = map[string]string{}
	if q, err := mysqlExtraParams(p.ExtraParams); err == nil {
		for k, v := range q {
			cfg.Params[k] = v
		}
	}

	return openAndPing(ctx, "mysql", cfg.FormatDSN())
}

// Schemas returns p.Database as the connection's single schema — MySQL has
// no separate schema concept (a "database" already is one), and the
// connection is already pinned to one via Open.
func (mysqlConnector) Schemas(ctx context.Context, db *sql.DB, p Params) ([]string, error) {
	return []string{p.Database}, nil
}

func (mysqlConnector) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
		FROM information_schema.tables WHERE table_schema = ?
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTables(rows)
}

func (mysqlConnector) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, column_type, is_nullable = 'YES', COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanColumns(rows)
}

func (mysqlConnector) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT index_name, column_name, non_unique = 0, index_name = 'PRIMARY'
		FROM information_schema.statistics WHERE table_schema = ? AND table_name = ?
		ORDER BY index_name, seq_in_index`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupIndexes(rows)
}

func (mysqlConnector) Constraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tc.constraint_name, tc.constraint_type, kcu.column_name,
		       kcu.referenced_table_schema, kcu.referenced_table_name, kcu.referenced_column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		     AND tc.table_name = kcu.table_name
		WHERE tc.table_schema = ? AND tc.table_name = ?
		ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupConstraints(rows)
}

func (mysqlConnector) Triggers(ctx context.Context, db *sql.DB, schema, table string) ([]Trigger, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT trigger_name, event_manipulation, action_timing
		FROM information_schema.triggers
		WHERE event_object_schema = ? AND event_object_table = ?
		ORDER BY trigger_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriggers(rows)
}

func (mysqlConnector) Rows(ctx context.Context, db *sql.DB, schema, table string, limit, offset int, sort string, sortDesc bool, filter string) (RowsResult, error) {
	q := "SELECT * FROM " + mysqlQuoteIdent(table)
	if filter != "" {
		q += " WHERE " + filter
	}
	if sort != "" {
		q += " ORDER BY " + mysqlQuoteIdent(sort)
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

func (mysqlConnector) Commit(ctx context.Context, db *sql.DB, schema, table string, edits []RowEdit) error {
	return buildAndExecCommit(ctx, db, mysqlQuoteIdent(table), edits, mysqlQuoteIdent, func(int) string { return "?" })
}

func (mysqlConnector) Query(ctx context.Context, db *sql.DB, sqlText string, limit int) (RowsResult, error) {
	return genericQuery(ctx, db, sqlText, limit)
}

// mysqlExtraParams parses a "key=value&key2=value2" free-form string into
// single-valued params, matching mysql.Config.Params' shape.
func mysqlExtraParams(extra string) (map[string]string, error) {
	q := map[string]string{}
	if extra == "" {
		return q, nil
	}
	values, err := parseExtraParams(extra)
	if err != nil {
		return nil, err
	}
	for k, vs := range values {
		if len(vs) > 0 {
			q[k] = vs[0]
		}
	}
	return q, nil
}
