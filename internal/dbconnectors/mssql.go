package dbconnectors

import (
	"context"
	"database/sql"
	"net/url"

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
