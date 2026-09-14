package runtimes

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/protocol"
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE projects, runtimes, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	u, err := auth.CreateUser(context.Background(), pool, "owner@example.com", "password123", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func TestRuntimeLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := testUser(t, pool)

	rt, token, err := Create(ctx, pool, "dev01", userID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rt.Status != StatusPending {
		t.Fatalf("Status = %q, want %q", rt.Status, StatusPending)
	}
	if token == "" {
		t.Fatal("expected non-empty enrollment token")
	}

	// The runtime looks itself up by the hash of the token it was given.
	found, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup by token hash: %v", err)
	}
	if found.ID != rt.ID {
		t.Fatalf("found.ID = %q, want %q", found.ID, rt.ID)
	}

	// An unknown token hash is not found.
	if _, err := GetByTokenHash(ctx, pool, auth.HashToken("bogus")); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	hello := protocol.HelloPayload{
		Hostname:       "dev01.local",
		OS:             "linux",
		Arch:           "amd64",
		RuntimeVersion: "0.1.0",
		Capabilities:   protocol.Capabilities{},
	}
	if err := MarkOnline(ctx, pool, rt.ID, hello); err != nil {
		t.Fatalf("mark online: %v", err)
	}

	online, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after online: %v", err)
	}
	if online.Status != StatusOnline {
		t.Fatalf("Status = %q, want %q", online.Status, StatusOnline)
	}
	if online.Hostname != hello.Hostname || online.OS != hello.OS || online.Arch != hello.Arch {
		t.Fatalf("hello fields not persisted: %+v", online)
	}
	if online.EnrolledAt == nil {
		t.Fatal("expected EnrolledAt to be set on first online transition")
	}
	firstEnrolledAt := *online.EnrolledAt

	if err := Heartbeat(ctx, pool, rt.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	afterHeartbeat, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after heartbeat: %v", err)
	}
	if afterHeartbeat.LastSeenAt == nil {
		t.Fatal("expected LastSeenAt to be set after heartbeat")
	}

	// Reconnecting (a second MarkOnline) must not move enrolled_at.
	if err := MarkOnline(ctx, pool, rt.ID, hello); err != nil {
		t.Fatalf("mark online again: %v", err)
	}
	reconnected, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after reconnect: %v", err)
	}
	if !reconnected.EnrolledAt.Equal(firstEnrolledAt) {
		t.Fatalf("EnrolledAt changed on reconnect: %v -> %v", firstEnrolledAt, *reconnected.EnrolledAt)
	}

	if err := MarkOffline(ctx, pool, rt.ID); err != nil {
		t.Fatalf("mark offline: %v", err)
	}
	offline, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after offline: %v", err)
	}
	if offline.Status != StatusOffline {
		t.Fatalf("Status = %q, want %q", offline.Status, StatusOffline)
	}

	list, err := List(ctx, pool)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v, want 1 runtime", list, err)
	}

	revoked, err := Revoke(ctx, pool, rt.ID)
	if err != nil || !revoked {
		t.Fatalf("revoke: revoked=%v err=%v", revoked, err)
	}
	afterRevoke, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after revoke: %v", err)
	}
	if afterRevoke.Status != StatusRevoked || afterRevoke.RevokedAt == nil {
		t.Fatalf("expected revoked status with RevokedAt set, got %+v", afterRevoke)
	}

	// MarkOffline must never resurrect a revoked runtime to "offline".
	if err := MarkOffline(ctx, pool, rt.ID); err != nil {
		t.Fatalf("mark offline after revoke: %v", err)
	}
	stillRevoked, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after post-revoke offline: %v", err)
	}
	if stillRevoked.Status != StatusRevoked {
		t.Fatalf("Status = %q, want it to remain %q", stillRevoked.Status, StatusRevoked)
	}

	// Revoking an unknown ID reports not-found.
	if found, err := Revoke(ctx, pool, "00000000-0000-0000-0000-000000000000"); err != nil || found {
		t.Fatalf("revoke unknown id: found=%v err=%v", found, err)
	}

	// Reactivating clears the revocation and reuses the original token: the
	// runtime can look itself up by the same token hash it always had.
	reactivated, err := Reactivate(ctx, pool, rt.ID)
	if err != nil || !reactivated {
		t.Fatalf("reactivate: reactivated=%v err=%v", reactivated, err)
	}
	afterReactivate, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after reactivate: %v", err)
	}
	if afterReactivate.Status != StatusPending || afterReactivate.RevokedAt != nil {
		t.Fatalf("expected pending status with RevokedAt cleared, got %+v", afterReactivate)
	}

	// The reactivated runtime can reconnect (MarkOnline) using its
	// original, never-reissued token.
	if err := MarkOnline(ctx, pool, rt.ID, hello); err != nil {
		t.Fatalf("mark online after reactivate: %v", err)
	}
	onlineAgain, err := GetByTokenHash(ctx, pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup after post-reactivate online: %v", err)
	}
	if onlineAgain.Status != StatusOnline {
		t.Fatalf("Status = %q, want %q", onlineAgain.Status, StatusOnline)
	}

	// Reactivating an unknown ID reports not-found.
	if found, err := Reactivate(ctx, pool, "00000000-0000-0000-0000-000000000000"); err != nil || found {
		t.Fatalf("reactivate unknown id: found=%v err=%v", found, err)
	}
}
