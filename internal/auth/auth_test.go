package auth

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testPool connects to a real Postgres instance for integration tests, or
// skips the test if DEVSTATIONONE_DATABASE_URL is not set (e.g. `make db-up`
// was not run). Each test truncates users/sessions so runs are isolated.
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

func TestBootstrapAdmin_IdempotentAndRequiresCredentials(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	logger := testLogger()

	if err := BootstrapAdmin(ctx, pool, logger, "", ""); err != nil {
		t.Fatalf("no-op bootstrap: %v", err)
	}
	if n, _ := CountUsers(ctx, pool); n != 0 {
		t.Fatalf("expected no users created without credentials, got %d", n)
	}

	if err := BootstrapAdmin(ctx, pool, logger, "admin@example.com", "supersecret"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if n, _ := CountUsers(ctx, pool); n != 1 {
		t.Fatalf("expected 1 user after bootstrap, got %d", n)
	}

	// Second call must not create a second admin or error.
	if err := BootstrapAdmin(ctx, pool, logger, "someone-else@example.com", "supersecret"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if n, _ := CountUsers(ctx, pool); n != 1 {
		t.Fatalf("expected bootstrap to stay a no-op once a user exists, got %d users", n)
	}
}
