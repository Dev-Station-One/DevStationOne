package dbconnectors

import (
	"context"
	"database/sql"

	"github.com/go-sql-driver/mysql"
)

type mysqlConnector struct{}

func (mysqlConnector) Open(ctx context.Context, p Params) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = p.Username
	cfg.Passwd = p.Password
	cfg.Net = "tcp"
	cfg.Addr = hostPort(p.Host, p.Port, 3306)
	cfg.DBName = p.Database
	// One of "true", "false", "skip-verify", "preferred", or a name
	// registered via mysql.RegisterTLSConfig — passed straight through, so
	// the connection form's SSL-mode field must use one of those values.
	if p.SSLMode != "" {
		cfg.TLSConfig = p.SSLMode
	}
	cfg.Params = map[string]string{}
	if q, err := mysqlExtraParams(p.ExtraParams); err == nil {
		for k, v := range q {
			cfg.Params[k] = v
		}
	}

	return openAndPing(ctx, "mysql", cfg.FormatDSN())
}

// mysqlExtraParams parses a "key=value&key2=value2" free-form string into
// single-valued params, matching mysql.Config.Params' shape.
func mysqlExtraParams(extra string) (map[string]string, error) {
	q := map[string]string{}
	if extra == "" {
		return q, nil
	}
	values, err := parseExtraParams(extra)
	if err != nil {
		return nil, err
	}
	for k, vs := range values {
		if len(vs) > 0 {
			q[k] = vs[0]
		}
	}
	return q, nil
}
