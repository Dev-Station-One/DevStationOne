package commandruns

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/commands"
	"devstationone/internal/database"
	"devstationone/internal/sessions"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DEVSTATIONONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATIONONE_TEST_DATABASE_URL not set; skipping integration test")
	}
	if err := database.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		"TRUNCATE command_runs, project_commands, terminal_sessions, projects, runtimes, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedFixtures(t *testing.T, pool *pgxpool.Pool) (projectID, commandID, userID string) {
	t.Helper()
	ctx := context.Background()
	u, err := auth.CreateUser(ctx, pool, "runner@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtimes (name, token_hash, status, created_by)
		VALUES ('dev01', 'runs-test-hash', 'online', $1)`, u.ID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		VALUES ('Runs Proj', (SELECT id FROM runtimes LIMIT 1), '/tmp', '/bin/bash', '{}', $1)
		RETURNING id`, u.ID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cmd, err := commands.Create(ctx, pool, projectID, commands.Fields{Name: "Build", Command: "make build", SortOrder: 0}, u.ID)
	if err != nil {
		t.Fatalf("seed command: %v", err)
	}
	return projectID, cmd.ID, u.ID
}

func seedSession(t *testing.T, pool *pgxpool.Pool, projectID, userID string) string {
	t.Helper()
	sess, err := sessions.Create(context.Background(), pool, projectID, "Build", 80, 24, userID, "xterm")
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess.ID
}

func TestLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.FinishedAt != nil || run.ExitCode != nil {
		t.Fatalf("fresh run = %+v, want running", run)
	}

	sessionID := seedSession(t, pool, projectID, userID)
	if err := SetSession(ctx, pool, run.ID, sessionID); err != nil {
		t.Fatalf("SetSession: %v", err)
	}

	code := 0
	if err := FinalizeSession(ctx, pool, sessionID, &code, "", "build ok\n"); err != nil {
		t.Fatalf("FinalizeSession: %v", err)
	}
	// A second finalize is a no-op (the row is already terminal) — the
	// runtime's session.exit and an explicit close can both fire.
	if err := FinalizeSession(ctx, pool, sessionID, nil, "late close", ""); err != nil {
		t.Fatalf("second FinalizeSession: %v", err)
	}

	got, err := GetForProject(ctx, pool, projectID, run.ID)
	if err != nil {
		t.Fatalf("GetForProject: %v", err)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 || got.OutputTail != "build ok\n" || got.Error != "" {
		t.Fatalf("finalized = %+v", got)
	}
	if got.FinishedAt == nil {
		t.Fatal("finished_at still NULL")
	}

	// Unknown session IDs finalize nothing (ordinary terminals).
	if err := FinalizeSession(ctx, pool, "00000000-0000-0000-0000-000000000000", nil, "x", ""); err != nil {
		t.Fatalf("no-op finalize errored: %v", err)
	}
}

func TestFinalizeSessionFailurePath(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	// A run whose session never started: FailRun finishes it directly.
	run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := FailRun(ctx, pool, run.ID, "runtime is offline"); err != nil {
		t.Fatalf("FailRun: %v", err)
	}
	got, err := GetForProject(ctx, pool, projectID, run.ID)
	if err != nil {
		t.Fatalf("GetForProject: %v", err)
	}
	if got.ExitCode != nil || got.Error != "runtime is offline" {
		t.Fatalf("failed run = %+v", got)
	}
}

func TestListForProjectScopesAndFilters(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	for i := 0; i < 3; i++ {
		run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
		if err != nil {
			t.Fatalf("Start[%d]: %v", i, err)
		}
		sessionID := seedSession(t, pool, projectID, userID)
		if err := SetSession(ctx, pool, run.ID, sessionID); err != nil {
			t.Fatalf("SetSession[%d]: %v", i, err)
		}
		if err := FinalizeSession(ctx, pool, sessionID, nil, "boom", ""); err != nil {
			t.Fatalf("FinalizeSession[%d]: %v", i, err)
		}
	}

	runs, total, err := ListForProject(ctx, pool, projectID, "", 25, 0)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if total != 3 || len(runs) != 3 {
		t.Fatalf("total=%d len=%d, want 3/3", total, len(runs))
	}
	// The list view never carries the output tail.
	for _, run := range runs {
		if run.OutputTail != "" {
			t.Fatalf("list row %s carried a tail", run.ID)
		}
	}

	// One run belongs to a second command; ?command_id= filters.
	second, err := commands.Create(ctx, pool, projectID, commands.Fields{Name: "Test", Command: "make test", SortOrder: 1}, userID)
	if err != nil {
		t.Fatalf("seed second command: %v", err)
	}
	if _, err := Start(ctx, pool, projectID, second.ID, "Test", "make test", userID); err != nil {
		t.Fatalf("Start second: %v", err)
	}
	runs, total, err = ListForProject(ctx, pool, projectID, second.ID, 25, 0)
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Name != "Test" {
		t.Fatalf("filtered = total %d, %+v", total, runs)
	}
}

func TestPruneKeepsNewestHundred(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	for i := 0; i < runsPerPage+5; i++ {
		if _, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID); err != nil {
			t.Fatalf("Start[%d]: %v", i, err)
		}
	}
	_, total, err := ListForProject(ctx, pool, projectID, "", 25, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != runsPerPage {
		t.Fatalf("total = %d, want pruned to %d", total, runsPerPage)
	}
}

// TestPruneDeletesRunSessions is the regression test for pruning: each run
// owns a terminal_sessions row, and dropping the run must drop that row too
// — otherwise it grows unbounded and re-enters ListInteractiveForProject
// once the NOT EXISTS check no longer matches.
func TestPruneDeletesRunSessions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	for i := 0; i < runsPerPage+5; i++ {
		run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
		if err != nil {
			t.Fatalf("Start[%d]: %v", i, err)
		}
		sessionID := seedSession(t, pool, projectID, userID)
		if err := SetSession(ctx, pool, run.ID, sessionID); err != nil {
			t.Fatalf("SetSession[%d]: %v", i, err)
		}
	}

	_, total, err := ListForProject(ctx, pool, projectID, "", 25, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != runsPerPage {
		t.Fatalf("total = %d, want pruned to %d", total, runsPerPage)
	}

	var sessionCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM terminal_sessions WHERE project_id = $1`, projectID,
	).Scan(&sessionCount); err != nil {
		t.Fatalf("session count: %v", err)
	}
	if sessionCount != runsPerPage {
		t.Fatalf("session rows = %d, want pruned to %d alongside the runs", sessionCount, runsPerPage)
	}
}

// TestDeletingCommandDeletesItsRuns is the regression test for the cascade
// delete change: deleting a command must remove its run history too, not
// orphan it (there is no UI path back to a run once its command's History
// expander is gone).
func TestDeletingCommandDeletesItsRuns(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, commandID, userID := seedFixtures(t, pool)

	run, err := Start(ctx, pool, projectID, commandID, "Build", "make build", userID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := commands.Delete(ctx, pool, commandID); err != nil {
		t.Fatalf("Delete command: %v", err)
	}

	if _, err := GetForProject(ctx, pool, projectID, run.ID); err != ErrNotFound {
		t.Fatalf("GetForProject after command delete = %v, want ErrNotFound", err)
	}
	_, total, err := ListForProject(ctx, pool, projectID, "", 25, 0)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0 — run should have been deleted with its command", total)
	}
}
