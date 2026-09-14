// Package testdb holds the fixed connection details for the real test
// database containers docker-compose.yml defines (started via `make
// db-up`/`make dbtest-up`), shared by every package whose tests connect to
// one of them.
package testdb

const (
	PostgresHost     = "localhost"
	PostgresPort     = 55432
	PostgresUser     = "devstation"
	PostgresPassword = "devstation"
	PostgresDatabase = "devstation"

	MySQLHost     = "localhost"
	MySQLPort     = 53306
	MySQLUser     = "devstation"
	MySQLPassword = "devstation"
	MySQLDatabase = "devstation"

	MSSQLHost     = "localhost"
	MSSQLPort     = 51433
	MSSQLUser     = "sa"
	MSSQLPassword = "Devstation1!"
)
