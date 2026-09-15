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
	"devstationone/internal/backups"
	"devstationone/internal/commandruns"
	"devstationone/internal/config"
	"devstationone/internal/database"
	"devstationone/internal/events"
	"devstationone/internal/logging"
	"devstationone/internal/notifications"
	"devstationone/internal/projects"
	"devstationone/internal/sessions"
)

// auditPurgeInterval is how often the retention policy is enforced
// (docs/IMPLEMENTATION_PLAN.md Slice 11) — daily is frequent enough for a
// day-granularity retention_days setting.
const auditPurgeInterval = 24 * time.Hour

// idleScanInterval is how often the awaiting-input idle heuristic scans
// attached sessions (docs/IMPLEMENTATION_PLAN.md Slice 15) — independent of
// DEVSTATIONONE_IDLE_NOTIFY_SECONDS, which sets the idle threshold itself.
const idleScanInterval = 10 * time.Second

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

	handler, hub, bus, _, registry := api.NewRouter(db, logger, "web/static", cfg.DBEncryptionKey, cfg.TurnstileSiteKey, cfg.TurnstileSecretKey)
	go idleNotifyLoop(ctx, hub, db, bus, logger, time.Duration(cfg.IdleNotifySeconds)*time.Second)
	go commandruns.RecoverLoop(ctx, db.Pool, hub, logger)
	go backups.RunScheduler(ctx, db.Pool, registry, cfg.DBEncryptionKey, logger)
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

// idleNotifyLoop implements the "awaiting input" notification heuristic
// (docs/IMPLEMENTATION_PLAN.md Slice 15): every idleScanInterval it asks hub
// which sessions have gone quiet for longer than threshold and records a
// notification for each one's project owner.
func idleNotifyLoop(ctx context.Context, hub *sessions.Hub, db *database.DB, bus *events.Bus, logger *slog.Logger, threshold time.Duration) {
	ticker := time.NewTicker(idleScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, sessionID := range hub.IdleSessions(threshold) {
				sess, err := sessions.Get(ctx, db.Pool, sessionID)
				if err != nil {
					logger.Error("idle notify: session lookup failed", "session_id", sessionID, "error", err)
					continue
				}
				proj, err := projects.Get(ctx, db.Pool, sess.ProjectID)
				if err != nil {
					logger.Error("idle notify: project lookup failed", "project_id", sess.ProjectID, "error", err)
					continue
				}
				notifications.Record(ctx, db.Pool, bus, proj.CreatedBy, notifications.TypeAwaitingInput,
					sess.Name+" is waiting for input", proj.Name,
					map[string]any{"session_id": sess.ID, "project_id": proj.ID})
			}
		}
	}
}

// recoverLoop finalizes Quick Command run rows stuck in their running
// state (ADR-045): a runtime that dies mid-run sends no session.exit, so
// the sweep finalizes them from the recorded session/runtime state. Runs
// whose runtime only blipped offline (under RecoverOfflineFor) are left
// alone — the runtime keeps its PTY sessions alive across a reconnect.
// The loop itself lives in internal/commandruns, next to the capture
// logic it serves.
