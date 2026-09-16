package commandruns

import (
	"context"
	"testing"

	"devstationone/internal/sessions"
)

func TestRecoverStale_SessionTerminal(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)
	hub := sessions.NewHub()

	run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionID := seedSession(t, pool, projectID, userID)
	if err := SetSession(ctx, pool, run.ID, sessionID); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	// The session row went terminal (close race, missed session.exit) while
	// the run row did not.
	if err := sessions.MarkExited(ctx, pool, sessionID, 3); err != nil {
		t.Fatalf("MarkExited: %v", err)
	}

	hub.RegisterCapture(sessionID)
	hub.Forward(sessionID, []byte("partial output"))

	n, err := RecoverStale(ctx, pool, hub, RecoverOfflineFor)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered = %d, want 1", n)
	}

	got, err := GetForProject(ctx, pool, projectID, run.ID)
	if err != nil {
		t.Fatalf("GetForProject: %v", err)
	}
	if got.ExitCode == nil || *got.ExitCode != 3 || got.FinishedAt == nil {
		t.Fatalf("recovered run = %+v, want exit 3 + finished", got)
	}
	// The Hub buffer was taken for this session — no leak, tail empty (the
	// session was terminal, the sweep did not find bytes in it).
	if tail := hub.TakeCapture(sessionID); tail != nil {
		t.Fatalf("capture leaked: %q", tail)
	}
}

func TestRecoverStale_RuntimeOfflineLongEnough(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionID := seedSession(t, pool, projectID, userID)
	if err := SetSession(ctx, pool, run.ID, sessionID); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	// The runtime died mid-run: no session.exit ever, and its last
	// heartbeat is older than RecoverOfflineFor.
	if _, err := pool.Exec(ctx, `
		UPDATE runtimes SET status = 'offline', last_seen_at = now() - $2::interval
		WHERE id = (SELECT runtime_id FROM projects WHERE id = $1::uuid)`,
		projectID, RecoverOfflineFor); err != nil {
		t.Fatalf("mark runtime offline: %v", err)
	}

	n, err := RecoverStale(ctx, pool, sessions.NewHub(), RecoverOfflineFor)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered = %d, want 1", n)
	}

	got, err := GetForProject(ctx, pool, projectID, run.ID)
	if err != nil {
		t.Fatalf("GetForProject: %v", err)
	}
	if got.ExitCode != nil || got.Error != "runtime went offline while running" {
		t.Fatalf("recovered run = %+v", got)
	}
}

func TestRecoverStale_LeavesLiveRunsAlone(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	// Runtime online, session running: a legitimate run in flight.
	if _, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Runtime offline but only briefly (recent last_seen_at): a blip — the
	// PTY sessions survive it, so the run is left alone.
	if _, err := pool.Exec(ctx, `
		UPDATE runtimes SET status = 'offline', last_seen_at = now()
		WHERE id = (SELECT runtime_id FROM projects WHERE id = $1)`, projectID); err != nil {
		t.Fatalf("mark runtime briefly offline: %v", err)
	}
	if _, err := Start(ctx, pool, projectID, commandID, "Blip", "make blip", userID); err != nil {
		t.Fatalf("Start blip: %v", err)
	}

	n, err := RecoverStale(ctx, pool, sessions.NewHub(), RecoverOfflineFor)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if n != 0 {
		t.Fatalf("recovered = %d, want 0", n)
	}

	_, total, err := ListForProject(ctx, pool, projectID, "", 25, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 still running", total)
	}
}
