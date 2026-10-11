package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/events"
	"devstationone/internal/notifications"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestValidEndpoint(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/abc",
	}
	for _, u := range ok {
		if err := ValidEndpoint(u); err != nil {
			t.Errorf("ValidEndpoint(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"", "not a url", "http://fcm.googleapis.com/x", "ftp://example.com/x",
		"https://localhost/x", "https://foo.localhost/x", "https://127.0.0.1/x",
		"https://10.0.0.5/x", "https://192.168.1.1/x", "https://169.254.169.254/latest",
		"https://[::1]/x", "https://user:pw@example.com/x", "https:///x",
	}
	for _, u := range bad {
		if err := ValidEndpoint(u); err == nil {
			t.Errorf("ValidEndpoint(%q) = nil, want error", u)
		}
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	ct, err := seal(testKey, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := open(testKey, ct)
	if err != nil || string(got) != "secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := open([]byte("ffffffffffffffffffffffffffffffff"), ct); err == nil {
		t.Fatal("open with wrong key succeeded")
	}
}

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
	if _, err := pool.Exec(context.Background(), "TRUNCATE push_subscriptions, push_vapid, notifications, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(context.Background(), pool, logger, testKey, "https://ds.example.com")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// browserKeys returns a valid p256dh/auth pair, as a browser would produce.
func browserKeys(t *testing.T) (p256dh, authSecret string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	if _, err := rand.Read(a); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(a)
}

func TestNewReusesStoredKeypair(t *testing.T) {
	pool := testPool(t)
	first := testService(t, pool)
	second := testService(t, pool)
	if first.PublicKey() == "" || first.PublicKey() != second.PublicKey() {
		t.Fatalf("public keys differ across restarts: %q vs %q", first.PublicKey(), second.PublicKey())
	}
	if first.privateKey != second.privateKey {
		t.Fatal("private key not stable across restarts")
	}
	if _, err := New(context.Background(), pool, slog.New(slog.NewTextHandler(io.Discard, nil)),
		[]byte("ffffffffffffffffffffffffffffffff"), "https://ds.example.com"); err == nil {
		t.Fatal("New with a different encryption key succeeded")
	}
}

func TestNotifyDeliversAndPrunesGoneSubscriptions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	svc := testService(t, pool)
	u, err := auth.CreateUser(ctx, pool, "push@example.com", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.CreateUser(ctx, pool, "other@example.com", "password123", false)
	if err != nil {
		t.Fatal(err)
	}

	var delivered atomic.Int32
	var gotAuth, gotEnc atomic.Value
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		gotEnc.Store(r.Header.Get("Content-Encoding"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer live.Close()
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer gone.Close()
	otherPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("push sent to a different user's subscription")
	}))
	defer otherPush.Close()

	p, a := browserKeys(t)
	for _, ep := range []struct{ user, url string }{{u.ID, live.URL}, {u.ID, gone.URL}, {other.ID, otherPush.URL}} {
		if err := svc.Subscribe(ctx, ep.user, ep.url, p, a, "test"); err != nil {
			t.Fatal(err)
		}
	}

	svc.Notify(ctx, notifications.Notification{ID: "n1", UserID: u.ID, Title: "bash finished", Body: "ok",
		Detail: map[string]any{"project_id": "p1"}})

	if delivered.Load() != 1 {
		t.Fatalf("live endpoint received %d pushes, want 1", delivered.Load())
	}
	if !strings.HasPrefix(gotAuth.Load().(string), "vapid ") {
		t.Fatalf("Authorization = %q, want a vapid header", gotAuth.Load())
	}
	if gotEnc.Load() != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q, want aes128gcm", gotEnc.Load())
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE user_id = $1`, u.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("user has %d subscriptions after a 410, want 1 (the live one)", n)
	}
}

func TestAttachForwardsNotificationCreated(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	svc := testService(t, pool)
	u, _ := auth.CreateUser(ctx, pool, "tap@example.com", "password123", false)

	got := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- struct{}{}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	p, a := browserKeys(t)
	if err := svc.Subscribe(ctx, u.ID, srv.URL, p, a, "test"); err != nil {
		t.Fatal(err)
	}

	bus := events.NewBus()
	svc.Attach(bus)
	// Real path: Record inserts the row and publishes on the bus.
	notifications.Record(ctx, pool, bus, u.ID, notifications.TypeTaskCompleted, "t", "b", nil)

	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("notifications.Record did not trigger a push")
	}
}

func TestSubscribeRehomesEndpointAndUnsubscribeIsScoped(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	svc := testService(t, pool)
	alice, _ := auth.CreateUser(ctx, pool, "alice@example.com", "password123", false)
	bob, _ := auth.CreateUser(ctx, pool, "bob@example.com", "password123", false)
	p, a := browserKeys(t)
	const ep = "https://fcm.googleapis.com/fcm/send/x"

	if err := svc.Subscribe(ctx, alice.ID, ep, p, a, "ua"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(ctx, bob.ID, ep, p, a, "ua"); err != nil {
		t.Fatal(err)
	}
	var owner string
	pool.QueryRow(ctx, `SELECT user_id FROM push_subscriptions WHERE endpoint = $1`, ep).Scan(&owner)
	if owner != bob.ID {
		t.Fatalf("endpoint owner = %s, want bob (re-subscribed)", owner)
	}

	if err := svc.Unsubscribe(ctx, alice.ID, ep); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n)
	if n != 1 {
		t.Fatal("alice removed bob's subscription")
	}
	if err := svc.Unsubscribe(ctx, bob.ID, ep); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n)
	if n != 0 {
		t.Fatal("bob could not remove his own subscription")
	}
}
