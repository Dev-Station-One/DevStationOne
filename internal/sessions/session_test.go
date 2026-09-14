package sessions

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/projects"
	"devstationone/internal/runtimes"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DEVSTATIONONE_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATIONONE_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE terminal_sessions, projects, runtimes, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var testProjectSeq int

func testProject(t *testing.T, pool *pgxpool.Pool) (projectID, userID string) {
	t.Helper()
	ctx := context.Background()
	testProjectSeq++
	email := fmt.Sprintf("owner%d@example.com", testProjectSeq)
	u, err := auth.CreateUser(ctx, pool, email, "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	rt, _, err := runtimes.Create(ctx, pool, "dev01", u.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	p, err := projects.Create(ctx, pool, projects.Fields{
		Name:             "App",
		RuntimeID:        rt.ID,
		WorkingDirectory: "/home/brian/app",
		Shell:            "/bin/bash",
	}, u.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID, u.ID
}

func TestSessionLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, userID := testProject(t, pool)

	s, err := Create(ctx, pool, projectID, "bash", 80, 24, userID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.Status != StatusRequested {
		t.Fatalf("status = %q, want %q", s.Status, StatusRequested)
	}

	if err := MarkStarting(ctx, pool, s.ID); err != nil {
		t.Fatalf("mark starting: %v", err)
	}
	if err := MarkDetached(ctx, pool, s.ID); err != nil {
		t.Fatalf("mark detached: %v", err)
	}
	if err := MarkRunning(ctx, pool, s.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	got, err := Get(ctx, pool, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("status = %q, want %q", got.Status, StatusRunning)
	}

	renamed, err := Rename(ctx, pool, s.ID, "claude")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "claude" {
		t.Fatalf("name = %q, want claude", renamed.Name)
	}

	if err := UpdateSize(ctx, pool, s.ID, 120, 40); err != nil {
		t.Fatalf("update size: %v", err)
	}
	got, _ = Get(ctx, pool, s.ID)
	if got.Cols != 120 || got.Rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", got.Cols, got.Rows)
	}

	if err := MarkExited(ctx, pool, s.ID, 1); err != nil {
		t.Fatalf("mark exited: %v", err)
	}
	got, _ = Get(ctx, pool, s.ID)
	if got.Status != StatusExited || got.ExitCode == nil || *got.ExitCode != 1 {
		t.Fatalf("after exit: status=%q exitCode=%v, want exited/1", got.Status, got.ExitCode)
	}
	if got.ClosedAt == nil {
		t.Fatal("expected closed_at to be set after exit")
	}

	// A terminal status (exited) must not be reopened by a stray
	// MarkDetached/MarkRunning racing a delayed message.
	if err := MarkDetached(ctx, pool, s.ID); err != nil {
		t.Fatalf("mark detached after exit: %v", err)
	}
	got, _ = Get(ctx, pool, s.ID)
	if got.Status != StatusExited {
		t.Fatalf("status after stray detach = %q, want still exited", got.Status)
	}
}

func TestSessionLifecycle_Failed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, userID := testProject(t, pool)

	s, err := Create(ctx, pool, projectID, "bash", 80, 24, userID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := MarkFailed(ctx, pool, s.ID, "runtime offline"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, err := Get(ctx, pool, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusFailed || got.ErrorMessage != "runtime offline" {
		t.Fatalf("got = %+v, want status failed with error message", got)
	}
}

func TestListForProject_ScopesAndOrders(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, userID := testProject(t, pool)
	otherProjectID, _ := testProject(t, pool)

	first, err := Create(ctx, pool, projectID, "bash", 80, 24, userID)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := Create(ctx, pool, projectID, "npm dev", 80, 24, userID)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := Create(ctx, pool, otherProjectID, "bash", 80, 24, userID); err != nil {
		t.Fatalf("create other project session: %v", err)
	}

	list, err := ListForProject(ctx, pool, projectID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list = %+v, want [second, first] scoped to projectID", list)
	}
}

func TestGet_NotFound(t *testing.T) {
	pool := testPool(t)
	if _, err := Get(context.Background(), pool, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
