package auth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapAdmin creates the first administrator account if the users table
// is empty and both email and password are provided. It is a no-op once any
// user exists, so it is safe to call on every startup.
func BootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, email, password string) error {
	count, err := CountUsers(ctx, pool)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	if email == "" || password == "" {
		logger.Warn("no admin account exists and no bootstrap credentials were provided; set DEVSTATIONONE_ADMIN_EMAIL and DEVSTATIONONE_ADMIN_PASSWORD")
		return nil
	}

	if _, err := CreateUser(ctx, pool, email, password, true); err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}
	logger.Info("bootstrap administrator created", "email", email)
	return nil
}
