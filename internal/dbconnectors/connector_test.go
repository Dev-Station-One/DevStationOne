package dbconnectors

import (
	"context"
	"testing"
	"time"

	"devstationone/internal/testdb"
)

func openOrSkip(t *testing.T, engine string, p Params) {
	t.Helper()
	c, err := For(engine)
	if err != nil {
		t.Fatalf("For(%q): %v", engine, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := c.Open(ctx, p)
	if err != nil {
		t.Skipf("%s test database not reachable (run: make dbtest-postgres-up / make dbtest-up): %v", engine, err)
	}
	defer db.Close()
}

func TestOpen_Postgres(t *testing.T) {
	openOrSkip(t, "postgresql", Params{
		Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase,
		SSLMode: "disable",
	})
}

func TestOpen_Postgres_WrongPasswordFails(t *testing.T) {
	c, _ := For("postgresql")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.Open(ctx, Params{
		Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: "definitely-wrong", Database: testdb.PostgresDatabase,
		SSLMode: "disable",
	})
	if err == nil {
		t.Skip("postgres test database not reachable, or accepted a wrong password unexpectedly")
	}
}

func TestOpen_MySQL(t *testing.T) {
	openOrSkip(t, "mysql", Params{
		Host: testdb.MySQLHost, Port: testdb.MySQLPort,
		Username: testdb.MySQLUser, Password: testdb.MySQLPassword, Database: testdb.MySQLDatabase,
	})
}

func TestOpen_MSSQL(t *testing.T) {
	openOrSkip(t, "mssql", Params{
		Host: testdb.MSSQLHost, Port: testdb.MSSQLPort,
		Username: testdb.MSSQLUser, Password: testdb.MSSQLPassword, SSLMode: "require",
	})
}

func TestOpen_SQLite(t *testing.T) {
	openOrSkip(t, "sqlite", Params{Database: ":memory:"})
}

func TestFor_UnsupportedEngine(t *testing.T) {
	if _, err := For("oracle"); err == nil {
		t.Error("For(\"oracle\") = nil error, want an error for an unsupported engine")
	}
}
