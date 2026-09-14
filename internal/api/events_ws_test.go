package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/auth"
)

func dialEventsStream(t *testing.T, server *httptest.Server, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/events/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}},
	})
	if err != nil {
		t.Fatalf("dial events stream: %v", err)
	}
	return conn
}

type wsEvent struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// readEvent reads messages off conn, skipping any (including the periodic
// "ping") that don't match wantType, until one does or timeout elapses.
func readEvent(t *testing.T, conn *websocket.Conn, wantType string, timeout time.Duration) wsEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			continue
		}
		var ev wsEvent
		if json.Unmarshal(data, &ev) == nil && ev.Type == wantType {
			return ev
		}
	}
	t.Fatalf("did not see event type %q within %s", wantType, timeout)
	return wsEvent{}
}

// assertNoEventOfType reads for window, failing the test if unwantedType
// ever arrives — used to prove another user's project.status stays scoped.
func assertNoEventOfType(t *testing.T, conn *websocket.Conn, unwantedType string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			continue
		}
		var ev wsEvent
		if json.Unmarshal(data, &ev) == nil && ev.Type == unwantedType {
			t.Fatalf("received unexpected event type %q: %+v", unwantedType, ev)
		}
	}
}

func TestEventsWS_RequiresAuth(t *testing.T) {
	_, server, _, _, _ := sessionsTestSetup(t)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/events/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, wsURL, nil); err == nil {
		t.Fatal("expected connecting without a session cookie to fail")
	}
}

func TestEventsWS_RuntimeStatusBroadcastToEveryUser(t *testing.T) {
	router, server, adminCookie, _, db := sessionsTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	adminEvents := dialEventsStream(t, server, adminCookie)
	defer adminEvents.Close(websocket.StatusNormalClosure, "")
	bobEvents := dialEventsStream(t, server, bobCookie)
	defer bobEvents.Close(websocket.StatusNormalClosure, "")

	// A second runtime, connected after both subscriptions exist — the
	// first runtime (from setup) already went online before anyone was
	// listening.
	body, _ := json.Marshal(map[string]string{"name": "dev02"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created runtimeWithTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startFakeRuntime(t, runCtx, server.URL, created.Token, "nobody", "/tmp", []string{"/bin/sh"})

	for name, conn := range map[string]*websocket.Conn{"admin": adminEvents, "bob": bobEvents} {
		ev := readEvent(t, conn, "runtime.status", 5*time.Second)
		if ev.Payload["runtime_id"] != created.ID || ev.Payload["online"] != true {
			t.Fatalf("%s: payload = %+v, want runtime_id=%q online=true", name, ev.Payload, created.ID)
		}
	}
}

func TestEventsWS_ProjectStatusScopedToOwner(t *testing.T) {
	router, server, adminCookie, projectID, db := sessionsTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	adminEvents := dialEventsStream(t, server, adminCookie)
	defer adminEvents.Close(websocket.StatusNormalClosure, "")
	bobEvents := dialEventsStream(t, server, bobCookie)
	defer bobEvents.Close(websocket.StatusNormalClosure, "")

	s := createSession(t, router, adminCookie, projectID, "bash")
	conn := dialSessionStream(t, server, adminCookie, s.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")

	ev := readEvent(t, adminEvents, "project.status", 5*time.Second)
	if ev.Payload["project_id"] != projectID {
		t.Fatalf("payload = %+v, want project_id %q", ev.Payload, projectID)
	}

	assertNoEventOfType(t, bobEvents, "project.status", 1*time.Second)
}
