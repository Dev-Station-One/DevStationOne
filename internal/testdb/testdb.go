// Package testdb holds the fixed connection details for the real test
// database containers docker-compose.yml defines (started via `make
// dbtest-postgres-up`/`make dbtest-up`), shared by every package whose
// tests connect to one of them.
package testdb

const (
	// Postgres points at the isolated test container (ADR-031), not the
	// persistent dev Control Server's database on 55432 — dbconnectors'
	// and internal/api's Postgres-connector tests create/drop real
	// fixture tables here.
	PostgresHost     = "localhost"
	PostgresPort     = 55433
	PostgresUser     = "devstation"
	PostgresPassword = "devstation"
	PostgresDatabase = "devstation_test"

	MySQLHost     = "localhost"
	MySQLPort     = 53306
	MySQLUser     = "devstation"
	MySQLPassword = "devstation"
	MySQLDatabase = "devstation"
	// MySQLRootUser/MySQLRootPassword: the official mysql image grants
	// 'root'@'%' full privileges WITH GRANT OPTION when MYSQL_ROOT_PASSWORD
	// is set — needed by dbconnectors' principal-management tests, since
	// MySQLUser above only has privileges on MySQLDatabase, not the global
	// CREATE USER/GRANT OPTION principal management itself requires.
	MySQLRootUser     = "root"
	MySQLRootPassword = "devstation"

	MSSQLHost     = "localhost"
	MSSQLPort     = 51433
	MSSQLUser     = "sa"
	MSSQLPassword = "Devstation1!"
)
