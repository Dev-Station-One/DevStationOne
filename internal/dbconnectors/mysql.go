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

func (mysqlConnector) SupportsUserManagement() bool { return true }

// mysqlPrincipal/mysqlSplitPrincipal round-trip a MySQL user's compound
// identity — MySQL keys an account by (user, host), unlike Postgres/MSSQL's
// single name — through Principal.Name as "user@host". A bare name (no "@")
// splits to host "%" (any host), matching CreatePrincipal's own default.
func mysqlPrincipal(user, host string) string { return user + "@" + host }

func mysqlSplitPrincipal(name string) (user, host string) {
	if i := strings.LastIndex(name, "@"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, "%"
}

// mysqlUserHostLiteral renders 'user'@'host' — the account-reference syntax
// CREATE/ALTER/DROP USER and GRANT/REVOKE all take in place of a plain
// identifier.
func mysqlUserHostLiteral(user, host string) string {
	return sqlLiteral(user) + "@" + sqlLiteral(host)
}

// Principals lists MySQL accounts, excluding MySQL's own built-in system
// accounts. CanLogin is always true — MySQL has no NOLOGIN concept the way
// Postgres roles do (ACCOUNT LOCK exists on newer versions but isn't
// universal across the MySQL/MariaDB versions this app targets).
func (mysqlConnector) Principals(ctx context.Context, db *sql.DB) ([]Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT User, Host, Super_priv = 'Y' FROM mysql.user
		WHERE User NOT IN ('', 'root', 'mysql.sys', 'mysql.session', 'mysql.infoschema')
		ORDER BY User, Host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Principal
	for rows.Next() {
		var user, host string
		var super bool
		if err := rows.Scan(&user, &host, &super); err != nil {
			return nil, err
		}
		out = append(out, Principal{Name: mysqlPrincipal(user, host), CanLogin: true, Superuser: super})
	}
	return out, rows.Err()
}

// Grants returns principal's own SHOW GRANTS output verbatim, one statement
// per row (Raw) — MySQL has no single information_schema view listing every
// grant type (table/database/global) the way Postgres does, and SHOW GRANTS
// is the standard way every MySQL client surfaces this.
func (mysqlConnector) Grants(ctx context.Context, db *sql.DB, principal string) ([]Grant, error) {
	user, host := mysqlSplitPrincipal(principal)
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR "+mysqlUserHostLiteral(user, host))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, Grant{Raw: raw})
	}
	return out, rows.Err()
}

func (mysqlConnector) CreatePrincipal(ctx context.Context, db *sql.DB, name, password string, canLogin, superuser bool) error {
	user, host := mysqlSplitPrincipal(name)
	if _, err := db.ExecContext(ctx, "CREATE USER "+mysqlUserHostLiteral(user, host)+" IDENTIFIED BY "+sqlLiteral(password)); err != nil {
		return err
	}
	if superuser {
		if _, err := db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON *.* TO "+mysqlUserHostLiteral(user, host)+" WITH GRANT OPTION"); err != nil {
			return fmt.Errorf("grant superuser privileges: %w", err)
		}
	}
	return nil
}

func (mysqlConnector) DropPrincipal(ctx context.Context, db *sql.DB, name string) error {
	user, host := mysqlSplitPrincipal(name)
	_, err := db.ExecContext(ctx, "DROP USER "+mysqlUserHostLiteral(user, host))
	return err
}

func (mysqlConnector) SetPrincipalPassword(ctx context.Context, db *sql.DB, name, password string) error {
	user, host := mysqlSplitPrincipal(name)
	_, err := db.ExecContext(ctx, "ALTER USER "+mysqlUserHostLiteral(user, host)+" IDENTIFIED BY "+sqlLiteral(password))
	return err
}

func (mysqlConnector) GrantPrivilege(ctx context.Context, db *sql.DB, principal, privilege, schema, table string) error {
	priv, ok := normalizePrivilege(privilege)
	if !ok {
		return fmt.Errorf("unsupported privilege %q", privilege)
	}
	user, host := mysqlSplitPrincipal(principal)
	q := "GRANT " + priv + " ON " + mysqlQuoteIdent(schema) + "." + mysqlQuoteIdent(table) + " TO " + mysqlUserHostLiteral(user, host)
	_, err := db.ExecContext(ctx, q)
	return err
}

func (mysqlConnector) RevokePrivilege(ctx context.Context, db *sql.DB, principal, privilege, schema, table string) error {
	priv, ok := normalizePrivilege(privilege)
	if !ok {
		return fmt.Errorf("unsupported privilege %q", privilege)
	}
	user, host := mysqlSplitPrincipal(principal)
	q := "REVOKE " + priv + " ON " + mysqlQuoteIdent(schema) + "." + mysqlQuoteIdent(table) + " FROM " + mysqlUserHostLiteral(user, host)
	_, err := db.ExecContext(ctx, q)
	return err
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
