package dbconnectors

import (
	"context"
	"database/sql"
	"net/url"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

type postgresConnector struct{}

func (postgresConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	u := url.URL{
		Scheme: "postgres",
		Host:   hostPort(p.Host, p.Port, 5432),
		Path:   "/" + p.Database,
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	q := url.Values{}
	if p.SSLMode != "" {
		q.Set("sslmode", p.SSLMode)
	}
	if p.SearchPath != "" {
		q.Set("search_path", p.SearchPath)
	}
	setExtraParams(q, p.ExtraParams)
	u.RawQuery = q.Encode()

	return openAndPing(ctx, "pgx", u.String())
}
