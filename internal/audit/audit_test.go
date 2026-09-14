package audit

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE audit_events, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE audit_settings SET retention_days = 90 WHERE id = 1"); err != nil {
		t.Fatalf("reset audit_settings: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	u, err := auth.CreateUser(context.Background(), pool, email, "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func TestRecordAndList(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := testUser(t, pool, "owner@example.com")

	Record(ctx, pool, EventProjectCreated, Actor{UserID: userID, Email: "owner@example.com"}, "project", "proj-1", map[string]any{"name": "App"}, "127.0.0.1")
	Record(ctx, pool, EventLoginFailed, Actor{Email: "attacker@example.com"}, "user", "", nil, "10.0.0.1")

	events, total, err := List(ctx, pool, Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	// Most recent first.
	if events[0].EventType != EventLoginFailed {
		t.Fatalf("events[0].EventType = %q, want %q", events[0].EventType, EventLoginFailed)
	}
	if events[0].ActorUserID != nil {
		t.Fatalf("failed-login ActorUserID = %v, want nil", events[0].ActorUserID)
	}
	if events[1].EventType != EventProjectCreated {
		t.Fatalf("events[1].EventType = %q, want %q", events[1].EventType, EventProjectCreated)
	}
	if events[1].ActorUserID == nil || *events[1].ActorUserID != userID {
		t.Fatalf("project-created ActorUserID = %v, want %q", events[1].ActorUserID, userID)
	}
	if events[1].Detail["name"] != "App" {
		t.Fatalf("Detail[name] = %v, want App", events[1].Detail["name"])
	}

	filtered, total, err := List(ctx, pool, Filter{EventType: EventLoginFailed})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if total != 1 || len(filtered) != 1 {
		t.Fatalf("filtered list = %d/%d, want 1/1", len(filtered), total)
	}
}

func TestSettingsAndPurge(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	s, err := GetSettings(ctx, pool)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if s.RetentionDays != 90 {
		t.Fatalf("default RetentionDays = %d, want 90", s.RetentionDays)
	}

	if _, err := UpdateSettings(ctx, pool, 0); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	Record(ctx, pool, EventLogout, Actor{UserID: testUser(t, pool, "u@example.com")}, "user", "", nil, "")
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET occurred_at = now() - interval '365 days'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// retention_days = 0 means keep forever: Purge must be a no-op.
	deleted, err := Purge(ctx, pool)
	if err != nil {
		t.Fatalf("purge (disabled): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("purge with retention disabled deleted %d rows, want 0", deleted)
	}

	if _, err := UpdateSettings(ctx, pool, 30); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	deleted, err = Purge(ctx, pool)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("purge deleted %d rows, want 1", deleted)
	}
	_, total, err := List(ctx, pool, Filter{})
	if err != nil {
		t.Fatalf("list after purge: %v", err)
	}
	if total != 0 {
		t.Fatalf("total after purge = %d, want 0", total)
	}
}
