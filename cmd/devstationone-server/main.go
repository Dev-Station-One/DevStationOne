// Command devstationone-server runs the DevStationOne Control Server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"devstationone/internal/api"
	"devstationone/internal/audit"
	"devstationone/internal/config"
	"devstationone/internal/database"
	"devstationone/internal/logging"
)

// auditPurgeInterval is how often the retention policy is enforced
// (docs/IMPLEMENTATION_PLAN.md Slice 11) — daily is frequent enough for a
// day-granularity retention_days setting.
const auditPurgeInterval = 24 * time.Hour

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	logger := logging.New(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := database.Migrate(cfg.DatabaseURL); err != nil {
		logger.Error("migration failed", "error", err)
		return err
	}

	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connect failed", "error", err)
		return err
	}
	defer db.Close()

	go auditPurgeLoop(ctx, db, logger)

	handler := api.NewRouter(db, logger, "web/static", cfg.DBEncryptionKey)
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.Addr, "env", cfg.Environment)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("server error", "error", err)
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		return err
	}
	logger.Info("server stopped")
	return nil
}

// auditPurgeLoop enforces the audit retention policy (internal/audit
// Settings.RetentionDays) once at startup and then on auditPurgeInterval,
// until ctx is cancelled.
func auditPurgeLoop(ctx context.Context, db *database.DB, logger *slog.Logger) {
	purge := func() {
		n, err := audit.Purge(ctx, db.Pool)
		if err != nil {
			logger.Error("audit purge failed", "error", err)
			return
		}
		if n > 0 {
			logger.Info("audit purge", "deleted", n)
		}
	}

	purge()
	ticker := time.NewTicker(auditPurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}
