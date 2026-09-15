// Recovery sweep for Quick Command run rows stuck in their running state
// (ADR-045): a runtime that dies mid-run sends no session.exit, so its
// command_runs rows would stay finished_at-NULL forever. The sweep
// finalizes them from the state that *is* recorded:
//
//   - the session row reached a terminal status but the run never got an
//     exit event (close race, missed message) → the session's own exit
//     code/error is used;
//   - the session's runtime has been offline (or revoked) longer than
//     RecoverOfflineFor → "runtime went offline while running", exit code
//     unknown. Shorter blips are left alone: the runtime keeps its PTY
//     sessions alive across a reconnect (PROTOCOL.md), so the process may
//     still finish normally and report session.exit.
//
// The captured output tail of an unreachable session is still in the Hub's
// in-memory buffer, which is why the sweep takes it there per session
// instead of recovering it in SQL. A session whose runtime row is already
// deleted is invisible to the join — such runs stay running, bounded by the
// 100-per-project prune.
package commandruns

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/sessions"
)

// RecoverScanInterval is how often cmd/devstationone-server's loop runs the
// sweep.
const RecoverScanInterval = 30 * time.Second

// RecoverOfflineFor is how long a runtime must have been offline before its
// still-running history rows are finalized as lost — long enough that the
// runtime's normal seconds-scale reconnect does not falsely fail live runs.
const RecoverOfflineFor = 2 * time.Minute

// RecoverLoop runs RecoverStale once at startup and then on
// RecoverScanInterval, until ctx is cancelled (wired in main.go like the
// other background loops).
func RecoverLoop(ctx context.Context, pool *pgxpool.Pool, hub *sessions.Hub, logger *slog.Logger) {
	sweep := func() {
		n, err := RecoverStale(ctx, pool, hub, RecoverOfflineFor)
		if err != nil {
			logger.Error("command run recovery failed", "error", err)
			return
		}
		if n > 0 {
			logger.Warn("command runs finalized after lost runtime", "count", n)
		}
	}

	sweep()
	ticker := time.NewTicker(RecoverScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// RecoverStale finalizes every still-running run whose session is terminal
// or whose runtime has been offline/revoked longer than offlineFor,
// returning how many rows it finished. FinalizeSession's finished_at-NULL
// guard makes repeated sweeps idempotent.
func RecoverStale(ctx context.Context, pool *pgxpool.Pool, hub *sessions.Hub, offlineFor time.Duration) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.id, r.session_id,
			s.exit_code, s.error_message,
			(s.status = 'failed') AS session_failed,
			(rt.status IN ('offline', 'revoked') AND rt.last_seen_at < now() - $1::interval) AS runtime_lost
		FROM command_runs r
		JOIN terminal_sessions s ON s.id = r.session_id
		JOIN projects p ON p.id = r.project_id
		JOIN runtimes rt ON rt.id = p.runtime_id
		WHERE r.finished_at IS NULL
			AND (
				s.status IN ('exited', 'failed', 'closed')
				OR (rt.status IN ('offline', 'revoked') AND rt.last_seen_at < now() - $1::interval)
			)
	`, offlineFor)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var recovered int
	for rows.Next() {
		var id, sessionID string
		var sessionExitCode *int
		var sessionErr *string
		var sessionFailed, runtimeLost bool
		if err := rows.Scan(&id, &sessionID, &sessionExitCode, &sessionErr, &sessionFailed, &runtimeLost); err != nil {
			return recovered, err
		}

		tail := Tail(hub.TakeCapture(sessionID))
		// The session's own terminal state is authoritative where it has
		// one; "runtime went offline" is only the fallback for a session
		// still marked running when its runtime vanished.
		var exitCode *int
		errMsg := "runtime went offline while running"
		if sessionFailed {
			errMsg = "session failed"
		}
		if sessionErr != nil && *sessionErr != "" {
			errMsg = *sessionErr
		}
		if sessionExitCode != nil {
			exitCode = sessionExitCode
			errMsg = ""
		}
		if err := FinalizeSession(ctx, pool, sessionID, exitCode, errMsg, tail); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, rows.Err()
}
