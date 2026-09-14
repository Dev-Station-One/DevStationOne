package dbconnectors

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/microsoft/go-mssqldb" // registers the "sqlserver" database/sql driver
)

type mssqlConnector struct{}

func (mssqlConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	u := url.URL{
		Scheme: "sqlserver",
		Host:   hostPort(p.Host, p.Port, 1433),
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	if p.InstanceName != "" {
		// A named instance replaces the port in go-mssqldb's URL form:
		// sqlserver://user:pass@host/instance?database=...
		u.Host = p.Host
		u.Path = "/" + p.InstanceName
	}
	q := url.Values{}
	if p.Database != "" {
		q.Set("database", p.Database)
	}
	switch p.SSLMode {
	case "", "disable":
		// No "encrypt" param: the driver's default (encrypted, but not
		// verified) applies.
	case "require":
		// Encrypted, but the server's certificate isn't verified — the
		// common case for a local/self-signed MSSQL instance.
		q.Set("encrypt", "true")
		q.Set("trustservercertificate", "true")
	case "verify":
		q.Set("encrypt", "true")
		q.Set("trustservercertificate", "false")
	default:
		q.Set("encrypt", p.SSLMode)
	}
	setExtraParams(q, p.ExtraParams)
	u.RawQuery = q.Encode()

	return openAndPing(ctx, "sqlserver", u.String())
}

// mssqlSystemSchemas are excluded from Schemas — fixed database roles each
// own a same-named schema, and INFORMATION_SCHEMA/sys aren't
// user-browsable.
var mssqlSystemSchemas = []string{
	"sys", "INFORMATION_SCHEMA", "db_owner", "db_accessadmin", "db_securityadmin",
	"db_ddladmin", "db_backupoperator", "db_datareader", "db_datawriter",
	"db_denydatareader", "db_denydatawriter", "guest",
}

func (mssqlConnector) Schemas(ctx context.Context, db *sql.DB, p Params) ([]string, error) {
	placeholders := make([]string, len(mssqlSystemSchemas))
	args := make([]any, len(mssqlSystemSchemas))
	for i, s := range mssqlSystemSchemas {
		placeholders[i] = fmt.Sprintf("@p%d", i+1)
		args[i] = s
	}
	rows, err := db.QueryContext(ctx,
		`SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN (`+strings.Join(placeholders, ",")+`) ORDER BY schema_name`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (mssqlConnector) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
		FROM information_schema.tables WHERE table_schema = @p1
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTables(rows)
}

func (mssqlConnector) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, data_type, CASE is_nullable WHEN 'YES' THEN 1 ELSE 0 END, COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns WHERE table_schema = @p1 AND table_name = @p2
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanColumns(rows)
}

func (mssqlConnector) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.name, c.name, i.is_unique, i.is_primary_key
		FROM sys.indexes i
		JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		JOIN sys.tables t ON t.object_id = i.object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE s.name = @p1 AND t.name = @p2 AND i.name IS NOT NULL
		ORDER BY i.name, ic.key_ordinal`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupIndexes(rows)
}

func (mssqlConnector) Constraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tc.constraint_name, tc.constraint_type, kcu.column_name,
		       ccu.table_schema, ccu.table_name, ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		     AND tc.table_name = kcu.table_name
		LEFT JOIN information_schema.referential_constraints rc
		  ON rc.constraint_name = tc.constraint_name AND rc.constraint_schema = tc.table_schema
		LEFT JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = rc.unique_constraint_name AND ccu.constraint_schema = rc.unique_constraint_schema
		WHERE tc.table_schema = @p1 AND tc.table_name = @p2
		ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupConstraints(rows)
}

func (mssqlConnector) Triggers(ctx context.Context, db *sql.DB, schema, table string) ([]Trigger, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT tr.name, '', CASE WHEN tr.is_instead_of_trigger = 1 THEN 'INSTEAD OF' ELSE 'AFTER' END
		FROM sys.triggers tr
		JOIN sys.tables t ON t.object_id = tr.parent_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE s.name = @p1 AND t.name = @p2
		ORDER BY tr.name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTriggers(rows)
}

func mssqlQuoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func mssqlPlaceholder(n int) string { return fmt.Sprintf("@p%d", n) }

func (mssqlConnector) Rows(ctx context.Context, db *sql.DB, schema, table string, limit, offset int, sort string, sortDesc bool, filter string) (RowsResult, error) {
	q := "SELECT * FROM " + mssqlQuoteIdent(schema) + "." + mssqlQuoteIdent(table)
	if filter != "" {
		q += " WHERE " + filter
	}
	// OFFSET/FETCH requires an ORDER BY; "(SELECT NULL)" is the standard
	// no-op ordering when the caller didn't ask for a real sort column.
	if sort != "" {
		q += " ORDER BY " + mssqlQuoteIdent(sort)
		if sortDesc {
			q += " DESC"
		}
	} else {
		q += " ORDER BY (SELECT NULL)"
	}
	q += fmt.Sprintf(" OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", offset, limit+1)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return RowsResult{}, err
	}
	defer rows.Close()
	return scanRows(rows, limit)
}

func (mssqlConnector) Commit(ctx context.Context, db *sql.DB, schema, table string, edits []RowEdit) error {
	return buildAndExecCommit(ctx, db, mssqlQuoteIdent(schema)+"."+mssqlQuoteIdent(table), edits, mssqlQuoteIdent, mssqlPlaceholder)
}

func (mssqlConnector) Query(ctx context.Context, db *sql.DB, sqlText string, limit int) (RowsResult, error) {
	return genericQuery(ctx, db, sqlText, limit)
}

func (mssqlConnector) SupportsUserManagement() bool { return true }

// Principals lists database users mapped to a server login, and database
// roles — excluding dbo/guest and every built-in fixed role (db_datareader
// etc., docs/IMPLEMENTATION_PLAN.md ADR-020 style: only what a caller could
// meaningfully manage). CanLogin is best-effort: NULL (no matching server
// login — e.g. a contained-database user) reads as false, mirroring how
// Triggers' event/timing are best-effort elsewhere in this package.
func (mssqlConnector) Principals(ctx context.Context, db *sql.DB) ([]Principal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT dp.name,
		       CASE WHEN sp.is_disabled = 0 THEN 1 ELSE 0 END,
		       CASE WHEN IS_SRVROLEMEMBER('sysadmin', dp.name) = 1 THEN 1 ELSE 0 END
		FROM sys.database_principals dp
		LEFT JOIN sys.server_principals sp ON sp.name = dp.name
		WHERE dp.type IN ('S', 'U')
		  AND dp.is_fixed_role = 0
		  AND dp.name NOT IN ('dbo', 'guest', 'INFORMATION_SCHEMA', 'sys')
		ORDER BY dp.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Principal
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.Name, &p.CanLogin, &p.Superuser); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (mssqlConnector) Grants(ctx context.Context, db *sql.DB, principal string) ([]Grant, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.permission_name, ISNULL(s.name, ''), ISNULL(o.name, '')
		FROM sys.database_permissions p
		LEFT JOIN sys.objects o ON o.object_id = p.major_id
		LEFT JOIN sys.schemas s ON s.schema_id = o.schema_id
		WHERE p.grantee_principal_id = DATABASE_PRINCIPAL_ID(@p1) AND p.state = 'G'
		ORDER BY p.permission_name, s.name, o.name`, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Privilege, &g.Schema, &g.Table); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreatePrincipal creates a paired server login + database user — MSSQL has
// no single statement for "a user who can log in", and CloudBeaver-style
// database user management conventionally manages the pair together rather
// than exposing bare server logins.
func (mssqlConnector) CreatePrincipal(ctx context.Context, db *sql.DB, name, password string, canLogin, superuser bool) error {
	if _, err := db.ExecContext(ctx, "CREATE LOGIN "+mssqlQuoteIdent(name)+" WITH PASSWORD = "+sqlLiteral(password)); err != nil {
		return fmt.Errorf("create login: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE USER "+mssqlQuoteIdent(name)+" FOR LOGIN "+mssqlQuoteIdent(name)); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	if !canLogin {
		if _, err := db.ExecContext(ctx, "ALTER LOGIN "+mssqlQuoteIdent(name)+" DISABLE"); err != nil {
			return fmt.Errorf("disable login: %w", err)
		}
	}
	if superuser {
		if _, err := db.ExecContext(ctx, "ALTER SERVER ROLE sysadmin ADD MEMBER "+mssqlQuoteIdent(name)); err != nil {
			return fmt.Errorf("grant sysadmin: %w", err)
		}
	}
	return nil
}

func (mssqlConnector) DropPrincipal(ctx context.Context, db *sql.DB, name string) error {
	// The database user must go first — MSSQL refuses to drop a login that
	// still owns a mapped user in the current database.
	if _, err := db.ExecContext(ctx, "DROP USER "+mssqlQuoteIdent(name)); err != nil {
		return fmt.Errorf("drop user: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DROP LOGIN "+mssqlQuoteIdent(name)); err != nil {
		return fmt.Errorf("drop login: %w", err)
	}
	return nil
}

func (mssqlConnector) SetPrincipalPassword(ctx context.Context, db *sql.DB, name, password string) error {
	_, err := db.ExecContext(ctx, "ALTER LOGIN "+mssqlQuoteIdent(name)+" WITH PASSWORD = "+sqlLiteral(password))
	return err
}

func (mssqlConnector) GrantPrivilege(ctx context.Context, db *sql.DB, principal, privilege, schema, table string) error {
	priv, ok := normalizePrivilege(privilege)
	if !ok {
		return fmt.Errorf("unsupported privilege %q", privilege)
	}
	q := "GRANT " + priv + " ON " + mssqlQuoteIdent(schema) + "." + mssqlQuoteIdent(table) + " TO " + mssqlQuoteIdent(principal)
	_, err := db.ExecContext(ctx, q)
	return err
}

func (mssqlConnector) RevokePrivilege(ctx context.Context, db *sql.DB, principal, privilege, schema, table string) error {
	priv, ok := normalizePrivilege(privilege)
	if !ok {
		return fmt.Errorf("unsupported privilege %q", privilege)
	}
	q := "REVOKE " + priv + " ON " + mssqlQuoteIdent(schema) + "." + mssqlQuoteIdent(table) + " FROM " + mssqlQuoteIdent(principal)
	_, err := db.ExecContext(ctx, q)
	return err
}
