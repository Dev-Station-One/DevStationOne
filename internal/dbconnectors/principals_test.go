package dbconnectors

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"devstationone/internal/testdb"
)

// assertUserManagementSuite exercises Principals/Grants/CreatePrincipal/
// DropPrincipal/SetPrincipalPassword/GrantPrivilege/RevokePrivilege against
// c, already opened as db (with sufficient privilege to manage other
// principals) and a target table already existing as schema.table.
// principal is a name/identity c can create without clashing with a
// preexisting account (mysqlPrincipal-formatted for the mysql connector).
func assertUserManagementSuite(t *testing.T, c Connector, db *sql.DB, schema, table, principal string) {
	t.Helper()
	ctx := context.Background()

	if !c.SupportsUserManagement() {
		t.Fatal("SupportsUserManagement() = false, want true")
	}

	if err := c.CreatePrincipal(ctx, db, principal, "Testpassw0rd!", true, false); err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	t.Cleanup(func() { c.DropPrincipal(context.Background(), db, principal) })

	find := func(t *testing.T) *Principal {
		t.Helper()
		list, err := c.Principals(ctx, db)
		if err != nil {
			t.Fatalf("Principals: %v", err)
		}
		for i := range list {
			if list[i].Name == principal {
				return &list[i]
			}
		}
		t.Fatalf("Principals = %+v, want it to include %s", list, principal)
		return nil
	}

	if p := find(t); !p.CanLogin || p.Superuser {
		t.Errorf("newly created principal = %+v, want CanLogin=true Superuser=false", p)
	}

	if err := c.SetPrincipalPassword(ctx, db, principal, "AnotherPassw0rd!"); err != nil {
		t.Fatalf("SetPrincipalPassword: %v", err)
	}

	if err := c.GrantPrivilege(ctx, db, principal, "select", schema, table); err != nil {
		t.Fatalf("GrantPrivilege: %v", err)
	}
	grants, err := c.Grants(ctx, db, principal)
	if err != nil {
		t.Fatalf("Grants (after grant): %v", err)
	}
	if len(grants) == 0 {
		t.Fatalf("Grants (after grant) = %+v, want at least one", grants)
	}

	if err := c.GrantPrivilege(ctx, db, principal, "not-a-real-privilege", schema, table); err == nil {
		t.Error("GrantPrivilege with an invalid privilege = nil error, want an error")
	}

	if err := c.RevokePrivilege(ctx, db, principal, "select", schema, table); err != nil {
		t.Fatalf("RevokePrivilege: %v", err)
	}

	if err := c.DropPrincipal(ctx, db, principal); err != nil {
		t.Fatalf("DropPrincipal: %v", err)
	}
	list, err := c.Principals(ctx, db)
	if err != nil {
		t.Fatalf("Principals (after drop): %v", err)
	}
	for _, p := range list {
		if p.Name == principal {
			t.Errorf("Principals (after drop) = %+v, still includes %s", list, principal)
		}
	}
}

func TestUserManagement_Postgres(t *testing.T) {
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

	assertUserManagementSuite(t, c, db, "public", "dbconn_test_parent", "dbconn_test_principal")
}

func TestUserManagement_MySQL(t *testing.T) {
	c, _ := For("mysql")
	ctx := context.Background()
	// Principal management needs global CREATE USER/GRANT OPTION, which
	// testdb.MySQLUser (scoped to testdb.MySQLDatabase only) doesn't have —
	// use the root account the official mysql image grants those to.
	db, err := c.Open(ctx, Params{
		Host: testdb.MySQLHost, Port: testdb.MySQLPort,
		Username: testdb.MySQLRootUser, Password: testdb.MySQLRootPassword, Database: testdb.MySQLDatabase,
	})
	if err != nil {
		t.Skipf("mysql test database not reachable: %v", err)
	}
	defer db.Close()
	setupMySQLFixture(t, ctx, db)

	assertUserManagementSuite(t, c, db, testdb.MySQLDatabase, "dbconn_test_parent", mysqlPrincipal("dbconn_test_principal", "%"))
}

func TestUserManagement_MSSQL(t *testing.T) {
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

	assertUserManagementSuite(t, c, db, "dbo", "dbconn_test_parent", "dbconn_test_principal")
}

func TestUserManagement_SQLite_Unsupported(t *testing.T) {
	c, _ := For("sqlite")
	ctx := context.Background()
	db, err := c.Open(ctx, Params{Database: t.TempDir() + "/users.db"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if c.SupportsUserManagement() {
		t.Error("SupportsUserManagement() = true, want false for sqlite")
	}
	if _, err := c.Principals(ctx, db); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Principals error = %v, want ErrUnsupported", err)
	}
	if _, err := c.Grants(ctx, db, "anyone"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Grants error = %v, want ErrUnsupported", err)
	}
	if err := c.CreatePrincipal(ctx, db, "anyone", "pw", true, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("CreatePrincipal error = %v, want ErrUnsupported", err)
	}
	if err := c.DropPrincipal(ctx, db, "anyone"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("DropPrincipal error = %v, want ErrUnsupported", err)
	}
	if err := c.SetPrincipalPassword(ctx, db, "anyone", "pw"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetPrincipalPassword error = %v, want ErrUnsupported", err)
	}
	if err := c.GrantPrivilege(ctx, db, "anyone", "SELECT", "", "dbconn_test_parent"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GrantPrivilege error = %v, want ErrUnsupported", err)
	}
	if err := c.RevokePrivilege(ctx, db, "anyone", "SELECT", "", "dbconn_test_parent"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("RevokePrivilege error = %v, want ErrUnsupported", err)
	}
}
