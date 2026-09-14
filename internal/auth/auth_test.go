package auth

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/database"
)

// testPool connects to a real Postgres instance for integration tests, or
// skips the test if DEVSTATIONONE_TEST_DATABASE_URL is not set (e.g.
// `make dbtest-postgres-up` was not run). Each test truncates
// users/sessions so runs are isolated.
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestCreateUser_DuplicateEmailRejected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if _, err := CreateUser(ctx, pool, "a@example.com", "password123", false); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := CreateUser(ctx, pool, "a@example.com", "password123", false)
	if err != ErrEmailTaken {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

func TestAuthenticate_WrongPasswordRejected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if _, err := CreateUser(ctx, pool, "b@example.com", "correct-password", false); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := Authenticate(ctx, pool, "b@example.com", "wrong-password"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := Authenticate(ctx, pool, "nobody@example.com", "whatever"); err != ErrNotFound {
		t.Fatalf("unknown email: err = %v, want ErrNotFound", err)
	}

	u, err := Authenticate(ctx, pool, "b@example.com", "correct-password")
	if err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if u.Email != "b@example.com" {
		t.Errorf("Email = %q, want b@example.com", u.Email)
	}
}

func TestSessionLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	u, err := CreateUser(ctx, pool, "c@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, err := CreateSession(ctx, pool, u.ID, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, gotUser, err := SessionAndUser(ctx, pool, token)
	if err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	if gotUser.ID != u.ID {
		t.Errorf("UserID = %q, want %q", gotUser.ID, u.ID)
	}

	sessions, err := ListSessions(ctx, pool, u.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %v, %v, want 1 session", sessions, err)
	}

	if err := DeleteSessionByToken(ctx, pool, token); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, _, err := SessionAndUser(ctx, pool, token); err != ErrNotFound {
		t.Fatalf("after delete, err = %v, want ErrNotFound", err)
	}
}

func TestUpsertAdmin_CreatesThenUpdates(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	u, created, err := UpsertAdmin(ctx, pool, "admin@example.com", "supersecret")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true on first upsert")
	}
	if !u.IsAdmin {
		t.Fatal("IsAdmin = false, want true")
	}
	if n, _ := CountUsers(ctx, pool); n != 1 {
		t.Fatalf("expected 1 user, got %d", n)
	}

	u2, created2, err := UpsertAdmin(ctx, pool, "admin@example.com", "newpassword")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if created2 {
		t.Fatal("created = true, want false on second upsert (same email)")
	}
	if u2.ID != u.ID {
		t.Fatalf("ID = %q, want %q (same row updated)", u2.ID, u.ID)
	}
	if n, _ := CountUsers(ctx, pool); n != 1 {
		t.Fatalf("expected still 1 user after update, got %d", n)
	}

	if _, err := Authenticate(ctx, pool, "admin@example.com", "newpassword"); err != nil {
		t.Fatalf("authenticate with updated password: %v", err)
	}
}
