package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/auth"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

func wsURLFor(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/api/v1/runtimes/connect"
}

func dialRuntime(t *testing.T, wsURL, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func sendHello(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	payload, _ := json.Marshal(protocol.HelloPayload{
		Hostname:       "dev01.local",
		OS:             "linux",
		Arch:           "amd64",
		RuntimeVersion: "0.1.0",
	})
	data, _ := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: protocol.MessageHello, Payload: payload})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("send hello: %v", err)
	}
}

func pollUntil(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestRuntimeWS_ConnectHeartbeatRevoke(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	admin, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	rt, token, err := runtimes.Create(ctx, db.Pool, "dev01", admin.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	server := httptest.NewServer(router)
	defer server.Close()
	wsURL := wsURLFor(server.URL)

	// A revoked/unknown token must not be allowed to connect at all.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer bogus-token"}},
	}); err == nil {
		t.Fatal("expected dial with bogus token to fail")
	} else if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus token: status = %d, want 401", resp.StatusCode)
	}

	conn := dialRuntime(t, wsURL, token)
	sendHello(t, conn)

	pollUntil(t, 2*time.Second, func() bool {
		got, err := runtimes.GetByTokenHash(ctx, db.Pool, auth.HashToken(token))
		return err == nil && got.Status == runtimes.StatusOnline && got.Hostname == "dev01.local"
	})

	// Heartbeat round-trip: server must keep the connection open and bump
	// last_seen_at.
	before, err := runtimes.GetByTokenHash(ctx, db.Pool, auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup before heartbeat: %v", err)
	}
	hbData, _ := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: protocol.MessageHeartbeat})
	if err := conn.Write(ctx, websocket.MessageText, hbData); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	pollUntil(t, 2*time.Second, func() bool {
		got, err := runtimes.GetByTokenHash(ctx, db.Pool, auth.HashToken(token))
		return err == nil && got.LastSeenAt != nil && !got.LastSeenAt.Before(*before.LastSeenAt)
	})

	// Revoke via the admin HTTP API while the runtime is connected.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginBody, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "password123"})
	loginResp, err := client.Post(server.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	loginResp.Body.Close()

	revokeResp, err := client.Post(server.URL+"/api/v1/runtimes/"+rt.ID+"/revoke", "application/json", nil)
	if err != nil {
		t.Fatalf("revoke request: %v", err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: status = %d", revokeResp.StatusCode)
	}

	// The server must push a MessageRevoked notice before closing.
	readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("expected revoked message before close: %v", err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != protocol.MessageRevoked {
		t.Fatalf("expected revoked envelope, got %s (err=%v)", data, err)
	}

	// Reconnecting with the now-revoked token must be rejected.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	}); err == nil {
		t.Fatal("expected dial with revoked token to fail")
	} else if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked token: status = %d, want 403", resp.StatusCode)
	}
}
