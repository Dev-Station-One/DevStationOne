---
id: databases
title: Databases
summary: Connect to, browse, and edit PostgreSQL, MySQL/MariaDB, SQL Server, and SQLite databases.
---
# Databases

Manage database connections scoped to this project, and browse their
contents without leaving the browser.

- Add a connection with **Add connection**, giving it a name and the
  credentials/host for PostgreSQL, MySQL/MariaDB, SQL Server, or SQLite.
- Once connected, explore catalogs/schemas, tables, columns, indexes, and
  constraints.
- The data browser supports filtering, sorting, and pagination, and lets
  you edit rows directly in the grid.
- The SQL editor runs arbitrary queries against the connection and shows
  results in the same grid.
- User/role/permission management is available where the database engine
  supports it — SQLite has no server-side users, so that section is hidden
  for SQLite connections rather than shown empty.

Credentials are never written to the audit log or exposed back to the
browser after they're saved.
