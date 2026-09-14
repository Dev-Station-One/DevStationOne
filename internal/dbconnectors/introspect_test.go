package dbconnectors

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"devstationone/internal/testdb"
)

// Every fixture below creates the same logical shape — a parent table, a
// child table with a foreign key + index on parent_id, and an AFTER INSERT
// trigger on the child — using each engine's own DDL dialect, so the same
// assertions can run against all four real engines.

func execAll(t *testing.T, ctx context.Context, db *sql.DB, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("fixture statement %q: %v", s, err)
		}
	}
}

func setupPostgresFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	execAll(t, ctx, db, []string{
		`DROP TABLE IF EXISTS dbconn_test_child`,
		`DROP TABLE IF EXISTS dbconn_test_parent`,
		`DROP FUNCTION IF EXISTS dbconn_test_noop() CASCADE`,
		`CREATE TABLE dbconn_test_parent (id serial PRIMARY KEY, name text NOT NULL)`,
		`CREATE TABLE dbconn_test_child (id serial PRIMARY KEY, parent_id int NOT NULL REFERENCES dbconn_test_parent(id), name text)`,
		`CREATE INDEX dbconn_test_child_parent_idx ON dbconn_test_child(parent_id)`,
		`CREATE FUNCTION dbconn_test_noop() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER dbconn_test_trg AFTER INSERT ON dbconn_test_child FOR EACH ROW EXECUTE FUNCTION dbconn_test_noop()`,
	})
	t.Cleanup(func() {
		execAllBestEffort(ctx, db, []string{`DROP TABLE IF EXISTS dbconn_test_child`, `DROP TABLE IF EXISTS dbconn_test_parent`, `DROP FUNCTION IF EXISTS dbconn_test_noop() CASCADE`})
	})
}

func setupMySQLFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	execAll(t, ctx, db, []string{
		`DROP TABLE IF EXISTS dbconn_test_child`,
		`DROP TABLE IF EXISTS dbconn_test_parent`,
		`CREATE TABLE dbconn_test_parent (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL)`,
		`CREATE TABLE dbconn_test_child (id INT AUTO_INCREMENT PRIMARY KEY, parent_id INT NOT NULL, name VARCHAR(100), INDEX dbconn_test_child_parent_idx (parent_id), FOREIGN KEY (parent_id) REFERENCES dbconn_test_parent(id))`,
		`CREATE TRIGGER dbconn_test_trg AFTER INSERT ON dbconn_test_child FOR EACH ROW SET @dbconn_test_noop = 1`,
	})
	t.Cleanup(func() {
		execAllBestEffort(ctx, db, []string{`DROP TABLE IF EXISTS dbconn_test_child`, `DROP TABLE IF EXISTS dbconn_test_parent`})
	})
}

func setupMSSQLFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	execAll(t, ctx, db, []string{
		`IF OBJECT_ID('dbo.dbconn_test_trg', 'TR') IS NOT NULL DROP TRIGGER dbo.dbconn_test_trg`,
		`IF OBJECT_ID('dbo.dbconn_test_child', 'U') IS NOT NULL DROP TABLE dbo.dbconn_test_child`,
		`IF OBJECT_ID('dbo.dbconn_test_parent', 'U') IS NOT NULL DROP TABLE dbo.dbconn_test_parent`,
		`CREATE TABLE dbo.dbconn_test_parent (id INT IDENTITY PRIMARY KEY, name NVARCHAR(100) NOT NULL)`,
		`CREATE TABLE dbo.dbconn_test_child (id INT IDENTITY PRIMARY KEY, parent_id INT NOT NULL REFERENCES dbo.dbconn_test_parent(id), name NVARCHAR(100))`,
		`CREATE INDEX dbconn_test_child_parent_idx ON dbo.dbconn_test_child(parent_id)`,
		`CREATE TRIGGER dbo.dbconn_test_trg ON dbo.dbconn_test_child AFTER INSERT AS BEGIN SET NOCOUNT ON; END`,
	})
	t.Cleanup(func() {
		execAllBestEffort(ctx, db, []string{
			`IF OBJECT_ID('dbo.dbconn_test_trg', 'TR') IS NOT NULL DROP TRIGGER dbo.dbconn_test_trg`,
			`IF OBJECT_ID('dbo.dbconn_test_child', 'U') IS NOT NULL DROP TABLE dbo.dbconn_test_child`,
			`IF OBJECT_ID('dbo.dbconn_test_parent', 'U') IS NOT NULL DROP TABLE dbo.dbconn_test_parent`,
		})
	})
}

func setupSQLiteFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	execAll(t, ctx, db, []string{
		`DROP TABLE IF EXISTS dbconn_test_child`,
		`DROP TABLE IF EXISTS dbconn_test_parent`,
		`CREATE TABLE dbconn_test_parent (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE dbconn_test_child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES dbconn_test_parent(id), name TEXT)`,
		`CREATE INDEX dbconn_test_child_parent_idx ON dbconn_test_child(parent_id)`,
		`CREATE TRIGGER dbconn_test_trg AFTER INSERT ON dbconn_test_child BEGIN SELECT 1; END`,
	})
}

func execAllBestEffort(ctx context.Context, db *sql.DB, stmts []string) {
	for _, s := range stmts {
		db.ExecContext(ctx, s)
	}
}

// assertIntrospection runs the shared set of checks against connector c,
// already opened as db, with tables living directly under schema (SQLite:
// pass "").
func assertIntrospection(t *testing.T, c Connector, db *sql.DB, schema string) {
	t.Helper()
	ctx := context.Background()

	tables, err := c.Tables(ctx, db, schema)
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	byName := make(map[string]Table, len(tables))
	for _, tb := range tables {
		byName[tb.Name] = tb
	}
	for _, want := range []string{"dbconn_test_parent", "dbconn_test_child"} {
		tb, ok := byName[want]
		if !ok {
			t.Fatalf("Tables = %v, want it to include %s", tables, want)
		}
		if tb.Kind != "table" {
			t.Errorf("table %s: Kind = %q, want table", want, tb.Kind)
		}
	}

	cols, err := c.Columns(ctx, db, schema, "dbconn_test_child")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	colNames := make([]string, len(cols))
	for i, col := range cols {
		colNames[i] = col.Name
	}
	for _, want := range []string{"id", "parent_id", "name"} {
		if !slices.Contains(colNames, want) {
			t.Errorf("Columns = %v, want it to include %q", colNames, want)
		}
	}

	idx, err := c.Indexes(ctx, db, schema, "dbconn_test_child")
	if err != nil {
		t.Fatalf("Indexes: %v", err)
	}
	foundParentIdx := false
	for _, ix := range idx {
		if slices.Contains(ix.Columns, "parent_id") {
			foundParentIdx = true
		}
	}
	if !foundParentIdx {
		t.Errorf("Indexes = %+v, want one covering parent_id", idx)
	}

	cons, err := c.Constraints(ctx, db, schema, "dbconn_test_child")
	if err != nil {
		t.Fatalf("Constraints: %v", err)
	}
	var haveFK, havePK bool
	for _, con := range cons {
		if con.Type == "foreign_key" {
			haveFK = true
			if con.References == "" {
				t.Errorf("foreign_key constraint %s has no References", con.Name)
			}
		}
		if con.Type == "primary_key" {
			havePK = true
		}
	}
	if !haveFK {
		t.Errorf("Constraints = %+v, want a foreign_key constraint", cons)
	}
	if !havePK {
		t.Errorf("Constraints = %+v, want a primary_key constraint", cons)
	}

	trig, err := c.Triggers(ctx, db, schema, "dbconn_test_child")
	if err != nil {
		t.Fatalf("Triggers: %v", err)
	}
	var haveTrigger bool
	for _, tr := range trig {
		if tr.Name == "dbconn_test_trg" {
			haveTrigger = true
		}
	}
	if !haveTrigger {
		t.Errorf("Triggers = %+v, want dbconn_test_trg", trig)
	}
}

func TestIntrospect_Postgres(t *testing.T) {
	c, _ := For("postgresql")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{
		Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase, SSLMode: "disable",
	})
	if err != nil {
		t.Skipf("postgres test database not reachable: %v", err)
	}
	defer db.Close()
	setupPostgresFixture(t, ctx, db)

	schemas, err := c.Schemas(ctx, db, Params{Database: testdb.PostgresDatabase})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	if !slices.Contains(schemas, "public") {
		t.Fatalf("Schemas = %v, want it to include public", schemas)
	}

	assertIntrospection(t, c, db, "public")
}

func TestIntrospect_MySQL(t *testing.T) {
	c, _ := For("mysql")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{
		Host: testdb.MySQLHost, Port: testdb.MySQLPort,
		Username: testdb.MySQLUser, Password: testdb.MySQLPassword, Database: testdb.MySQLDatabase,
	})
	if err != nil {
		t.Skipf("mysql test database not reachable: %v", err)
	}
	defer db.Close()
	setupMySQLFixture(t, ctx, db)

	schemas, err := c.Schemas(ctx, db, Params{Database: testdb.MySQLDatabase})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	if len(schemas) != 1 || schemas[0] != testdb.MySQLDatabase {
		t.Fatalf("Schemas = %v, want [%s]", schemas, testdb.MySQLDatabase)
	}

	assertIntrospection(t, c, db, testdb.MySQLDatabase)
}

func TestIntrospect_MSSQL(t *testing.T) {
	c, _ := For("mssql")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{
		Host: testdb.MSSQLHost, Port: testdb.MSSQLPort,
		Username: testdb.MSSQLUser, Password: testdb.MSSQLPassword, SSLMode: "require",
	})
	if err != nil {
		t.Skipf("mssql test database not reachable: %v", err)
	}
	defer db.Close()
	setupMSSQLFixture(t, ctx, db)

	schemas, err := c.Schemas(ctx, db, Params{})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	if !slices.Contains(schemas, "dbo") {
		t.Fatalf("Schemas = %v, want it to include dbo", schemas)
	}

	assertIntrospection(t, c, db, "dbo")
}

func TestIntrospect_SQLite(t *testing.T) {
	c, _ := For("sqlite")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{Database: t.TempDir() + "/introspect.db"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	setupSQLiteFixture(t, ctx, db)

	schemas, err := c.Schemas(ctx, db, Params{})
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	if schemas != nil {
		t.Errorf("Schemas = %v, want nil for sqlite", schemas)
	}

	assertIntrospection(t, c, db, "")
}
