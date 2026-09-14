package notifications

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/events"
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE notifications, users CASCADE"); err != nil {
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

func TestRecordListAndUnreadCount(t *testing.T) {
	pool := testPool(t)
	bus := events.NewBus()
	ctx := context.Background()
	userID := testUser(t, pool, "owner@example.com")
	otherID := testUser(t, pool, "other@example.com")

	ch, cancel := bus.Subscribe(userID)
	defer cancel()

	Record(ctx, pool, bus, userID, TypeTaskCompleted, "bash finished", "Exited successfully", map[string]any{"session_id": "s1"})
	Record(ctx, pool, bus, otherID, TypeTaskCompleted, "other's session", "Exited successfully", nil)

	select {
	case ev := <-ch:
		if ev.Type != "notification.created" {
			t.Fatalf("event type = %q, want notification.created", ev.Type)
		}
		n, ok := ev.Payload.(Notification)
		if !ok || n.Title != "bash finished" {
			t.Fatalf("payload = %+v, want Notification with title bash finished", ev.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive notification.created event")
	}

	list, total, err := ListForUser(ctx, pool, userID, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("list = %d/%d, want 1/1 (scoped to userID)", len(list), total)
	}
	if list[0].Detail["session_id"] != "s1" {
		t.Fatalf("Detail[session_id] = %v, want s1", list[0].Detail["session_id"])
	}
	if list[0].ReadAt != nil {
		t.Fatalf("ReadAt = %v, want nil (unread)", list[0].ReadAt)
	}

	unread, err := UnreadCount(ctx, pool, userID)
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}
}

func TestMarkReadAndMarkAllRead(t *testing.T) {
	pool := testPool(t)
	bus := events.NewBus()
	ctx := context.Background()
	userID := testUser(t, pool, "owner@example.com")
	otherID := testUser(t, pool, "other@example.com")

	Record(ctx, pool, bus, userID, TypeAwaitingInput, "bash is waiting", "My App", nil)
	Record(ctx, pool, bus, userID, TypeAwaitingInput, "npm dev is waiting", "My App", nil)
	list, _, err := ListForUser(ctx, pool, userID, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}

	// Another user cannot mark someone else's notification read.
	ok, err := MarkRead(ctx, pool, otherID, list[0].ID)
	if err != nil {
		t.Fatalf("mark read (wrong user): %v", err)
	}
	if ok {
		t.Fatal("MarkRead succeeded for a notification owned by a different user")
	}

	ok, err = MarkRead(ctx, pool, userID, list[0].ID)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if !ok {
		t.Fatal("MarkRead = false, want true")
	}
	// Idempotent: marking again still reports success.
	ok, err = MarkRead(ctx, pool, userID, list[0].ID)
	if err != nil || !ok {
		t.Fatalf("mark read again: ok=%v err=%v, want true/nil", ok, err)
	}

	unread, err := UnreadCount(ctx, pool, userID)
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread after one MarkRead = %d, want 1", unread)
	}

	n, err := MarkAllRead(ctx, pool, userID)
	if err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkAllRead affected %d rows, want 1", n)
	}
	unread, err = UnreadCount(ctx, pool, userID)
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread after MarkAllRead = %d, want 0", unread)
	}
}
