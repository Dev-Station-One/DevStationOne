package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devstationone/internal/auth"
)

// createRuntimeAs registers a runtime record but does not connect it — the
// runtime stays offline. Use createOnlineRuntime when a test needs to
// create or edit a project against it.
func createRuntimeAs(t *testing.T, router http.Handler, cookie *http.Cookie, name string) (id, token string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create runtime: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created runtimeWithTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}
	return created.ID, created.Token
}

// createOnlineRuntime registers a runtime, connects a fake agent advertising
// homeDir as its home directory and "/bin/bash" as its only shell, and waits
// until the Control Server sees it online — Project create/update now
// require a live, capability-reporting runtime.
func createOnlineRuntime(t *testing.T, router http.Handler, server *httptest.Server, cookie *http.Cookie, name, homeDir string) string {
	t.Helper()
	id, token := createRuntimeAs(t, router, cookie, name)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startFakeRuntime(t, runCtx, server.URL, token, "test-user", homeDir, []string{"/bin/bash"})

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var list []runtimeResponse
		json.NewDecoder(rec.Body).Decode(&list)
		for _, rt := range list {
			if rt.ID == id && rt.Online {
				return true
			}
		}
		return false
	})
	return id
}

// projectBody builds a create/update request for a project rooted at
// homeDir/src/my-application, which the caller must create on disk before
// use — Project creation now stats it through the runtime's real file
// manager.
func projectBody(runtimeID, homeDir string) []byte {
	body, _ := json.Marshal(projectRequest{
		Name:             "My Application",
		RuntimeID:        runtimeID,
		WorkingDirectory: filepath.Join(homeDir, "src", "my-application"),
		Shell:            "/bin/bash",
		Environment:      map[string]string{"NODE_ENV": "development"},
	})
	return body
}

func TestProjectHTTP_ActiveSessionsCount(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	getProject := func() projectResponse {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var p projectResponse
		json.NewDecoder(rec.Body).Decode(&p)
		return p
	}

	if p := getProject(); p.ActiveSessions != 0 {
		t.Fatalf("active_sessions before any session = %d, want 0", p.ActiveSessions)
	}

	s := createSession(t, router, cookie, projectID, "bash")
	if p := getProject(); p.ActiveSessions != 1 {
		t.Fatalf("active_sessions after create = %d, want 1", p.ActiveSessions)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	var list []projectResponse
	if err := json.NewDecoder(listRec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ActiveSessions != 1 {
		t.Fatalf("list = %+v, want one project with active_sessions=1", list)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+s.ID, nil)
	delReq.AddCookie(cookie)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete session: status = %d, body = %s", delRec.Code, delRec.Body.String())
	}

	if p := getProject(); p.ActiveSessions != 0 {
		t.Fatalf("active_sessions after delete = %d, want 0", p.ActiveSessions)
	}

	// Opening a session advances last_accessed_at (setup already opened
	// one, so it starts non-nil).
	prev := getProject().LastAccessedAt
	if prev == nil {
		t.Fatal("last_accessed_at before session open = nil, want set by setup")
	}
	createSession(t, router, cookie, projectID, "bash")
	if now := getProject().LastAccessedAt; now == nil || !now.After(*prev) {
		t.Fatalf("last_accessed_at did not advance: prev=%v now=%v", prev, now)
	}
}

func TestProjectHTTP_CreateListScopedByOwner(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := auth.CreateUser(ctx, db.Pool, "alice@example.com", "password123", false); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if _, err := auth.CreateUser(ctx, db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	adminCookie := loginAs(t, router, "admin@example.com", "password123")
	aliceCookie := loginAs(t, router, "alice@example.com", "password123")
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	homeDir := t.TempDir()
	runtimeID := createOnlineRuntime(t, router, server, adminCookie, "dev01", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "src", "my-application"), 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	// Alice creates a project.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody(runtimeID, homeDir)))
	req.AddCookie(aliceCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created projectResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Environment["NODE_ENV"] != "development" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	// Alice sees her own project, with her name as owner.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(aliceCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var aliceList []projectResponse
	json.NewDecoder(rec.Body).Decode(&aliceList)
	if len(aliceList) != 1 || aliceList[0].ID != created.ID {
		t.Fatalf("alice list = %+v, want her own project", aliceList)
	}
	if aliceList[0].OwnerName != "alice@example.com" {
		t.Fatalf("owner_name = %q, want alice@example.com", aliceList[0].OwnerName)
	}

	// Bob sees Alice's project too — the list is server-wide.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var bobList []projectResponse
	json.NewDecoder(rec.Body).Decode(&bobList)
	if len(bobList) != 1 || bobList[0].ID != created.ID {
		t.Fatalf("bob list = %+v, want alice's project", bobList)
	}

	// Bob can fetch Alice's project directly.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob get alice's project: status = %d, want 200", rec.Code)
	}

	// Bob cannot delete Alice's project; it looks not-found, not forbidden.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete alice's project: status = %d, want 404", rec.Code)
	}

	// Bob cannot patch Alice's project either.
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+created.ID, bytes.NewReader([]byte(`{"name":"Hijacked"}`)))
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob patch alice's project: status = %d, want 404", rec.Code)
	}

	// The admin sees every project across users.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var adminList []projectResponse
	json.NewDecoder(rec.Body).Decode(&adminList)
	if len(adminList) != 1 {
		t.Fatalf("admin list = %+v, want 1 project", adminList)
	}
}

func TestProjectHTTP_UpdateAndDelete(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	adminCookie := loginAs(t, router, "admin@example.com", "password123")

	homeDir := t.TempDir()
	runtimeID := createOnlineRuntime(t, router, server, adminCookie, "dev01", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "src", "my-application"), 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody(runtimeID, homeDir)))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created projectResponse
	json.NewDecoder(rec.Body).Decode(&created)

	patch, _ := json.Marshal(map[string]string{"name": "Renamed"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+created.ID, bytes.NewReader(patch))
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched projectResponse
	json.NewDecoder(rec.Body).Decode(&patched)
	if patched.Name != "Renamed" || patched.WorkingDirectory != created.WorkingDirectory {
		t.Fatalf("patched = %+v, want name Renamed with other fields preserved", patched)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
}

func TestProjectHTTP_ValidatesRuntimeAndFields(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	adminCookie := loginAs(t, router, "admin@example.com", "password123")

	// Missing required fields.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: status = %d, want 400", rec.Code)
	}

	// Runtime that does not exist.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody("00000000-0000-0000-0000-000000000000", t.TempDir())))
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown runtime: status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}

	// Unauthenticated requests are rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: status = %d, want 401", rec.Code)
	}
}
