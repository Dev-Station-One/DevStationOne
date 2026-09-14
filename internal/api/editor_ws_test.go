package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/database"
)

// editorTestSetup is filesTestSetup's shape for editor-stream tests: the
// Project rooted at a dedicated temp directory, plus the real httptest
// server URL so the test can dial WebSockets.
func editorTestSetup(t *testing.T) (serverURL string, cookie *http.Cookie, projectID, dir string, db *database.DB) {
	t.Helper()
	dir = t.TempDir()
	_, server, adminCookie, createdProjectID, testDB := sessionsTestSetupIn(t, dir)
	return server.URL, adminCookie, createdProjectID, dir, testDB
}

func dialEditorStream(t *testing.T, serverURL string, cookie *http.Cookie, projectID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(serverURL)+"/api/v1/projects/"+projectID+"/editor/stream",
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}}})
	if err != nil {
		t.Fatalf("dial editor stream: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func wsURL(serverURL string) string {
	return "ws://" + serverURL[len("http://"):]
}

func editorSend(t *testing.T, conn *websocket.Conn, msg map[string]any) {
	t.Helper()
	data, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("send %v: %v", msg, err)
	}
}

// editorRecv reads one server->browser editor message, failing on timeout.
func editorRecv(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read editor message: %v", err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			continue // skip pings etc.
		}
		if m["type"] == "ping" {
			continue
		}
		return m
	}
}

func waitFor(t *testing.T, conn *websocket.Conn, wantType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		m := editorRecv(t, conn)
		if m["type"] == wantType {
			return m
		}
	}
	t.Fatalf("timed out waiting for a %q message", wantType)
	return nil
}

func TestEditorStream_OpenWatchPush(t *testing.T) {
	serverURL, cookie, projectID, dir, _ := editorTestSetup(t)
	conn := dialEditorStream(t, serverURL, cookie, projectID)

	// Open: content + fingerprint for an existing file.
	os.WriteFile(filepath.Join(dir, "n.txt"), []byte("v1"), 0o644)
	editorSend(t, conn, map[string]any{"type": "open", "path": "n.txt"})
	m := waitFor(t, conn, "content")
	if m["data"] != "v1" || m["path"] != "n.txt" {
		t.Errorf("open reply = %v", m)
	}
	if _, ok := m["mtime"]; !ok || m["size"].(float64) != 2 {
		t.Errorf("open reply fingerprint = %v", m)
	}

	// Watch + external modify → content push (the runtime polls every ~1s;
	// the sleep lets the poller seed the path before the edit lands).
	editorSend(t, conn, map[string]any{"type": "watch", "paths": []string{"n.txt"}})
	time.Sleep(2200 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "n.txt"), []byte("v2 pushed"), 0o644)
	m = waitFor(t, conn, "content")
	if m["path"] != "n.txt" || m["data"] != "v2 pushed" {
		t.Errorf("push = %v", m)
	}

	// Save: with the fresh fingerprint from the push, no stale.
	editorSend(t, conn, map[string]any{
		"type": "save", "path": "n.txt", "content": "saved bytes",
		"base_mtime": m["mtime"], "base_size": m["size"],
	})
	m = waitFor(t, conn, "saved")
	if m["path"] != "n.txt" {
		t.Errorf("saved reply = %v", m)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "n.txt"))
	if string(got) != "saved bytes" {
		t.Errorf("disk after save = %q", got)
	}

	// Save again with a stale base (disk moved on): stale reply carries the
	// pre-write content, and the write still lands.
	os.WriteFile(filepath.Join(dir, "n.txt"), []byte("external"), 0o644)
	editorSend(t, conn, map[string]any{
		"type": "save", "path": "n.txt", "content": "mine",
		"base_mtime": m["mtime"], "base_size": m["size"],
	})
	m = waitFor(t, conn, "stale")
	if m["data"] != "external" {
		t.Errorf("stale reply pre-write content = %v", m)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "n.txt"))
	if string(got) != "mine" {
		t.Errorf("disk after stale save = %q", got)
	}

	// Deleting a watched file pushes a deleted message.
	os.Remove(filepath.Join(dir, "n.txt"))
	m = waitFor(t, conn, "deleted")
	if m["path"] != "n.txt" {
		t.Errorf("deleted reply = %v", m)
	}
}

func TestEditorStream_FanOutToBothConnections(t *testing.T) {
	serverURL, cookie, projectID, dir, _ := editorTestSetup(t)
	conn1 := dialEditorStream(t, serverURL, cookie, projectID)
	conn2 := dialEditorStream(t, serverURL, cookie, projectID)

	// The file must exist before it is watched: a never-observed path's
	// first poll seeds silently (creation of an unobserved path is not a
	// change the editor cares about).
	os.WriteFile(filepath.Join(dir, "fan.txt"), []byte("v1"), 0o644)
	editorSend(t, conn1, map[string]any{"type": "watch", "paths": []string{"fan.txt"}})
	editorSend(t, conn2, map[string]any{"type": "watch", "paths": []string{"fan.txt"}})
	time.Sleep(2200 * time.Millisecond) // let both watches seed

	os.WriteFile(filepath.Join(dir, "fan.txt"), []byte("changed"), 0o644)
	for i, conn := range []*websocket.Conn{conn1, conn2} {
		m := waitFor(t, conn, "content")
		if m["data"] != "changed" {
			t.Errorf("subscriber %d got %v", i, m)
		}
	}
}

func TestEditorStream_BinaryOpen(t *testing.T) {
	serverURL, cookie, projectID, dir, _ := editorTestSetup(t)
	conn := dialEditorStream(t, serverURL, cookie, projectID)

	os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("bin\x00ary"), 0o644)
	editorSend(t, conn, map[string]any{"type": "open", "path": "blob.bin"})
	m := waitFor(t, conn, "error")
	if m["error"] == nil || m["path"] != "blob.bin" {
		t.Errorf("binary open reply = %v", m)
	}
}

func TestEditorStream_RequiresProjectAccess(t *testing.T) {
	serverURL, cookie, _, _, _ := editorTestSetup(t)

	// A project ID that doesn't exist must not upgrade the socket.
	conn, _, err := websocket.Dial(context.Background(),
		wsURL(serverURL)+"/api/v1/projects/00000000-0000-0000-0000-000000000000/editor/stream",
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}}})
	if err == nil {
		conn.CloseNow()
		t.Error("dialing a nonexistent project's editor stream should fail")
	}
}

func TestEditorState_HTTP(t *testing.T) {
	router, cookie, projectID, _, _ := filesTestSetup(t)

	// Fresh project: empty state object.
	rec := fileReq(t, router, cookie, http.MethodGet, "/api/v1/projects/"+projectID+"/editor/state", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get state: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		State map[string]any `json:"state"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.State == nil || len(resp.State) != 0 {
		t.Errorf("fresh state response = %s", rec.Body.String())
	}

	// PUT then GET round-trips.
	rec = fileReq(t, router, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/editor/state",
		[]byte(`{"state":{"tabs":[{"path":"src/main.go","anchor":12,"head":12}],"active":"src/main.go"}}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put state: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = fileReq(t, router, cookie, http.MethodGet, "/api/v1/projects/"+projectID+"/editor/state", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.State["tabs"].([]any)) != 1 {
		t.Errorf("state round-trip = %s", rec.Body.String())
	}

	// An array-shaped state is storable: the server treats it as opaque.
	rec = fileReq(t, router, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/editor/state", []byte(`{"state":[1,2,3]}`))
	if rec.Code != http.StatusNoContent {
		t.Errorf("array-shaped state (server treats it as opaque): status = %d", rec.Code)
	}

	// Oversized state is rejected.
	big := make([]byte, editorStateMaxBytes+100)
	for i := range big {
		big[i] = 'x'
	}
	rec = fileReq(t, router, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/editor/state", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized state: status = %d, want 413", rec.Code)
	}
}
