package dbconnectors

import (
	"context"
	"database/sql"
	"testing"

	"devstationone/internal/testdb"
)

// assertRowsSuite exercises Rows/Commit/Query identically against any
// engine's dbconn_test_parent(id, name) fixture (see introspect_test.go's
// setup*Fixture, already run by the caller). id is an auto-increment
// primary key on every engine, so inserts only ever set name — explicitly
// inserting id would need engine-specific handling (MSSQL's
// SET IDENTITY_INSERT, in particular) that a real client wouldn't do for
// an auto-increment column either.
func assertRowsSuite(t *testing.T, c Connector, db *sql.DB, schema string) {
	t.Helper()
	ctx := context.Background()

	res, err := c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "", false, "")
	if err != nil {
		t.Fatalf("Rows (empty): %v", err)
	}
	if len(res.Rows) != 0 || res.HasMore {
		t.Fatalf("Rows (empty) = %+v, want no rows", res)
	}

	for _, name := range []string{"alice", "bob", "carol"} {
		if err := c.Commit(ctx, db, schema, "dbconn_test_parent", []RowEdit{
			{Op: "insert", Values: map[string]any{"name": name}},
		}); err != nil {
			t.Fatalf("Commit insert %s: %v", name, err)
		}
	}

	idCol, nameCol := -1, -1
	find := func(res RowsResult) {
		for i, col := range res.Columns {
			if col == "id" {
				idCol = i
			}
			if col == "name" {
				nameCol = i
			}
		}
	}

	res, err = c.Rows(ctx, db, schema, "dbconn_test_parent", 2, 0, "id", false, "")
	if err != nil {
		t.Fatalf("Rows (page 1): %v", err)
	}
	find(res)
	if len(res.Rows) != 2 || !res.HasMore {
		t.Fatalf("Rows (page 1) = %+v, want 2 rows and HasMore=true", res)
	}

	res, err = c.Rows(ctx, db, schema, "dbconn_test_parent", 2, 2, "id", false, "")
	if err != nil {
		t.Fatalf("Rows (page 2): %v", err)
	}
	if len(res.Rows) != 1 || res.HasMore {
		t.Fatalf("Rows (page 2) = %+v, want 1 row and HasMore=false", res)
	}

	res, err = c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "id", true, "")
	if err != nil {
		t.Fatalf("Rows (sort desc): %v", err)
	}
	if len(res.Rows) != 3 || res.Rows[0][nameCol] != "carol" {
		t.Fatalf("Rows (sort desc) = %+v, want carol first", res)
	}

	// bob's id, needed for the filter/update/delete checks below.
	res, err = c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "", false, "name = 'bob'")
	if err != nil {
		t.Fatalf("Rows (filter): %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][nameCol] != "bob" {
		t.Fatalf("Rows (filter name='bob') = %+v, want just bob", res)
	}
	bobID := res.Rows[0][idCol]

	if err := c.Commit(ctx, db, schema, "dbconn_test_parent", []RowEdit{
		{Op: "update", Original: map[string]any{"id": bobID}, Values: map[string]any{"name": "robert"}},
	}); err != nil {
		t.Fatalf("Commit update: %v", err)
	}
	res, _ = c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "", false, "name = 'robert'")
	if len(res.Rows) != 1 {
		t.Fatalf("after update = %+v, want one row named robert", res)
	}

	if err := c.Commit(ctx, db, schema, "dbconn_test_parent", []RowEdit{
		{Op: "delete", Original: map[string]any{"id": bobID}},
	}); err != nil {
		t.Fatalf("Commit delete: %v", err)
	}
	res, _ = c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "", false, "")
	if len(res.Rows) != 2 {
		t.Fatalf("after delete = %+v, want 2 remaining rows", res)
	}

	// Query: raw SELECT and raw INSERT.
	qres, err := c.Query(ctx, db, "SELECT id, name FROM dbconn_test_parent WHERE name = 'alice'", 10)
	if err != nil {
		t.Fatalf("Query select: %v", err)
	}
	if qres.IsExec || len(qres.Rows) != 1 {
		t.Fatalf("Query select = %+v, want one row", qres)
	}

	qres, err = c.Query(ctx, db, "INSERT INTO dbconn_test_parent (name) VALUES ('dave')", 10)
	if err != nil {
		t.Fatalf("Query insert: %v", err)
	}
	if !qres.IsExec {
		t.Fatalf("Query insert = %+v, want IsExec=true", qres)
	}
	res, _ = c.Rows(ctx, db, schema, "dbconn_test_parent", 10, 0, "", false, "name = 'dave'")
	if len(res.Rows) != 1 {
		t.Fatalf("after Query insert = %+v, want dave", res)
	}
}

func TestRows_Postgres(t *testing.T) {
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
	assertRowsSuite(t, c, db, "public")
}

func TestRows_MySQL(t *testing.T) {
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
	assertRowsSuite(t, c, db, testdb.MySQLDatabase)
}

func TestRows_MSSQL(t *testing.T) {
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
	assertRowsSuite(t, c, db, "dbo")
}

func TestRows_SQLite(t *testing.T) {
	c, _ := For("sqlite")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{Database: t.TempDir() + "/rows.db"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	setupSQLiteFixture(t, ctx, db)
	assertRowsSuite(t, c, db, "")
}
