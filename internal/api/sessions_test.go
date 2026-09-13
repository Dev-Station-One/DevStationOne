package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"devstation/internal/agent"
	"devstation/internal/auth"
	"devstation/internal/connection"
	"devstation/internal/database"
	"devstation/internal/protocol"
)

// startFakeRuntime dials the Control Server's runtime WebSocket endpoint
// using the real agent connection + session-manager code (so PTYs it
// starts are real OS PTYs, not simulated), and keeps it connected until
// ctx is cancelled.
func startFakeRuntime(t *testing.T, ctx context.Context, serverURL, token string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hello := protocol.HelloPayload{
		Hostname:       "dev01.local",
		OS:             "linux",
		Arch:           "amd64",
		RuntimeVersion: "test",
		Capabilities:   protocol.Capabilities{Terminal: true},
	}
	mgr := agent.NewManager(logger)
	go func() {
		_ = connection.Run(ctx, serverURL, token, hello, mgr, logger)
	}()
}

func sessionsTestSetup(t *testing.T) (router http.Handler, server *httptest.Server, adminCookie *http.Cookie, projectID string, db *database.DB) {
	t.Helper()
	return sessionsTestSetupIn(t, os.TempDir())
}

// sessionsTestSetupIn is sessionsTestSetup with the Project's working
// directory (and therefore the file manager's containment root) chosen by
// the caller. sessions_test uses os.TempDir(); file-manager tests pass a
// dedicated t.TempDir().
func sessionsTestSetupIn(t *testing.T, workingDir string) (router http.Handler, server *httptest.Server, adminCookie *http.Cookie, projectID string, db *database.DB) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router = NewRouter(db, logger, t.TempDir())
	server = httptest.NewServer(router)
	t.Cleanup(server.Close)

	adminCookie = loginAs(t, router, "admin@example.com", "password123")

	cur, err := user.Current()
	if err != nil {
		t.Fatalf("determine current OS user: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created runtimeWithTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startFakeRuntime(t, runCtx, server.URL, created.Token)

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var list []runtimeResponse
		json.NewDecoder(rec.Body).Decode(&list)
		for _, rt := range list {
			if rt.ID == created.ID && rt.Online {
				return true
			}
		}
		return false
	})

	projReq, _ := json.Marshal(projectRequest{
		Name: "App", RuntimeID: created.ID, OSUser: cur.Username,
		WorkingDirectory: workingDir, Shell: "/bin/sh",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projReq))
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var proj projectResponse
	json.NewDecoder(rec.Body).Decode(&proj)

	return router, server, adminCookie, proj.ID, db
}

func createSession(t *testing.T, router http.Handler, cookie *http.Cookie, projectID, name string) termSessionResponse {
	t.Helper()
	body, _ := json.Marshal(termSessionRequest{Name: name, Cols: 80, Rows: 24})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/sessions", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session %q: status = %d, body = %s", name, rec.Code, rec.Body.String())
	}
	var s termSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return s
}

func dialSessionStream(t *testing.T, server *httptest.Server, cookie *http.Cookie, sessionID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/sessions/" + sessionID + "/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}},
	})
	if err != nil {
		t.Fatalf("dial session stream: %v", err)
	}
	return conn
}

func writeInput(t *testing.T, conn *websocket.Conn, data string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(data)); err != nil {
		t.Fatalf("write input: %v", err)
	}
}

func readUntilContains(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out bytes.Buffer
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err == nil {
			out.Write(data)
			if strings.Contains(out.String(), want) {
				return out.String()
			}
		}
	}
	t.Fatalf("did not see %q within %s; got %q", want, timeout, out.String())
	return ""
}

func TestSessionHTTP_CreateAttachEcho(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)

	s := createSession(t, router, cookie, projectID, "bash")
	if s.Status != "detached" {
		t.Fatalf("status after create = %q, want detached (alive, unattached)", s.Status)
	}

	conn := dialSessionStream(t, server, cookie, s.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeInput(t, conn, "echo hello-from-session\n")
	readUntilContains(t, conn, "hello-from-session", 5*time.Second)
}

func TestSessionHTTP_TwoSimultaneousSessionsDoNotCrossTalk(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)

	s1 := createSession(t, router, cookie, projectID, "bash")
	s2 := createSession(t, router, cookie, projectID, "claude code")

	c1 := dialSessionStream(t, server, cookie, s1.ID)
	defer c1.Close(websocket.StatusNormalClosure, "")
	c2 := dialSessionStream(t, server, cookie, s2.ID)
	defer c2.Close(websocket.StatusNormalClosure, "")

	writeInput(t, c1, "echo from-session-one\n")
	writeInput(t, c2, "echo from-session-two\n")

	got1 := readUntilContains(t, c1, "from-session-one", 5*time.Second)
	got2 := readUntilContains(t, c2, "from-session-two", 5*time.Second)

	if strings.Contains(got1, "from-session-two") {
		t.Fatal("session one's stream leaked session two's output")
	}
	if strings.Contains(got2, "from-session-one") {
		t.Fatal("session two's stream leaked session one's output")
	}
}

func TestSessionHTTP_ReconnectPreservesProcess(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)

	s := createSession(t, router, cookie, projectID, "bash")

	first := dialSessionStream(t, server, cookie, s.ID)
	writeInput(t, first, "echo before-disconnect\n")
	readUntilContains(t, first, "before-disconnect", 5*time.Second)
	first.Close(websocket.StatusNormalClosure, "browser closed")

	// After the browser disconnects, the session must show detached, not
	// exited: the process must survive.
	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var got termSessionResponse
		json.NewDecoder(rec.Body).Decode(&got)
		return got.Status == "detached"
	})

	second := dialSessionStream(t, server, cookie, s.ID)
	defer second.Close(websocket.StatusNormalClosure, "")
	writeInput(t, second, "echo still-alive-after-reconnect\n")
	readUntilContains(t, second, "still-alive-after-reconnect", 5*time.Second)
}

func TestSessionHTTP_SignalKillsProcess(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)

	s := createSession(t, router, cookie, projectID, "bash")
	conn := dialSessionStream(t, server, cookie, s.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")
	writeInput(t, conn, "sleep 30\n")
	time.Sleep(300 * time.Millisecond) // let sh fork sleep before signalling

	sigBody, _ := json.Marshal(map[string]string{"signal": "SIGKILL"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+s.ID+"/signal", bytes.NewReader(sigBody))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("signal: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	pollUntil(t, 5*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var got termSessionResponse
		json.NewDecoder(rec.Body).Decode(&got)
		return got.Status == "exited"
	})
}

func TestSessionHTTP_ResizePersistsSize(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)
	_ = server

	s := createSession(t, router, cookie, projectID, "bash")

	body, _ := json.Marshal(map[string]int{"cols": 120, "rows": 40})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+s.ID+"/resize", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resize: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var got termSessionResponse
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Cols != 120 || got.Rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", got.Cols, got.Rows)
	}
}

func TestSessionHTTP_DeleteClosesSession(t *testing.T) {
	router, server, cookie, projectID, _ := sessionsTestSetup(t)
	_ = server

	s := createSession(t, router, cookie, projectID, "bash")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+s.ID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var got termSessionResponse
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Status != "closed" {
		t.Fatalf("status after delete = %q, want closed", got.Status)
	}

	// Attaching a closed session must be rejected.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/sessions/" + s.ID + "/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}},
	}); err == nil {
		t.Fatal("expected attaching a closed session to fail")
	}
}

func TestSessionHTTP_OwnershipEnforced(t *testing.T) {
	router, _, adminCookie, projectID, db := sessionsTestSetup(t)

	// Bob did not create this project, so he must not see its sessions.
	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	s := createSession(t, router, adminCookie, projectID, "bash")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
	req.AddCookie(bobCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob get admin's session: status = %d, want 404", rec.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/sessions", nil)
	listReq.AddCookie(bobCookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusNotFound {
		t.Fatalf("bob list sessions for admin's project: status = %d, want 404", listRec.Code)
	}
}
