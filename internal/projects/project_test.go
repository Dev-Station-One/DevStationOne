package projects

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstation/internal/auth"
	"devstation/internal/runtimes"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DEVSTATION_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATION_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE projects, runtimes, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
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

func testRuntime(t *testing.T, pool *pgxpool.Pool, createdByUserID string) string {
	t.Helper()
	rt, _, err := runtimes.Create(context.Background(), pool, "dev01", createdByUserID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	return rt.ID
}

func TestProjectLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := testUser(t, pool, "owner@example.com")
	runtimeID := testRuntime(t, pool, userID)

	fields := Fields{
		Name:             "My Application",
		RuntimeID:        runtimeID,
		OSUser:           "brian",
		WorkingDirectory: "/home/brian/src/my-application",
		Shell:            "/bin/bash",
		Environment:      map[string]string{"NODE_ENV": "development"},
	}

	created, err := Create(ctx, pool, fields, userID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected a generated ID")
	}
	if created.Environment["NODE_ENV"] != "development" {
		t.Fatalf("Environment = %+v, want NODE_ENV=development", created.Environment)
	}

	found, err := Get(ctx, pool, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.Name != fields.Name || found.OSUser != fields.OSUser {
		t.Fatalf("found = %+v, want fields %+v", found, fields)
	}

	if _, err := Get(ctx, pool, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("get unknown: err = %v, want ErrNotFound", err)
	}

	otherUserID := testUser(t, pool, "other@example.com")
	if _, err := Create(ctx, pool, fields, otherUserID); err != nil {
		t.Fatalf("create for other user: %v", err)
	}

	own, err := ListForUser(ctx, pool, userID)
	if err != nil || len(own) != 1 {
		t.Fatalf("ListForUser = %v, %v, want 1 project", own, err)
	}

	all, err := ListAll(ctx, pool)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAll = %v, %v, want 2 projects", all, err)
	}

	fields.Name = "Renamed"
	fields.Environment = map[string]string{}
	updated, err := Update(ctx, pool, created.ID, fields)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("Name = %q, want Renamed", updated.Name)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance: created=%v updated=%v", created.UpdatedAt, updated.UpdatedAt)
	}

	if _, err := Update(ctx, pool, "00000000-0000-0000-0000-000000000000", fields); err != ErrNotFound {
		t.Fatalf("update unknown: err = %v, want ErrNotFound", err)
	}

	deleted, err := Delete(ctx, pool, created.ID)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, err := Get(ctx, pool, created.ID); err != ErrNotFound {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}

	deletedAgain, err := Delete(ctx, pool, created.ID)
	if err != nil || deletedAgain {
		t.Fatalf("delete unknown: deleted=%v err=%v", deletedAgain, err)
	}
}
