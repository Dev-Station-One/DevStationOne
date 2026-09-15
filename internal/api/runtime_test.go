package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
)

func loginAs(t *testing.T, router http.Handler, email, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: status = %d, body = %s", email, rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login %s: expected 1 cookie, got %d", email, len(cookies))
	}
	return cookies[0]
}

func TestRuntimeHTTP_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := auth.CreateUser(ctx, db.Pool, "user@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "")

	userCookie := loginAs(t, router, "user@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(userCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: status = %d, want 403", rec.Code)
	}

	// Listing runtimes is intentionally open to any authenticated user: a
	// non-admin needs it to select a runtime when creating a Project.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-admin list: status = %d, want 200", rec.Code)
	}
}

func TestRuntimeHTTP_CreateListRevoke(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "")
	adminCookie := loginAs(t, router, "admin@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created runtimeWithTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Token == "" || created.Status != "pending" || created.Online {
		t.Fatalf("unexpected create response: %+v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	var list []runtimeResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created runtime", list)
	}

	// Revoking an unknown runtime is a 404.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/00000000-0000-0000-0000-000000000000/revoke", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown: status = %d, want 404", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+created.ID+"/revoke", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&list)
	if list[0].Status != "revoked" {
		t.Fatalf("Status = %q, want revoked", list[0].Status)
	}

	// Reactivating an unknown runtime is a 404.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/00000000-0000-0000-0000-000000000000/reactivate", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reactivate unknown: status = %d, want 404", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+created.ID+"/reactivate", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reactivate: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&list)
	if list[0].Status != "pending" {
		t.Fatalf("Status after reactivate = %q, want pending", list[0].Status)
	}
}

func TestRuntimeHTTP_ReactivateRequiresAdmin(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := auth.CreateUser(ctx, db.Pool, "user@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "")
	adminCookie := loginAs(t, router, "admin@example.com", "password123")
	userCookie := loginAs(t, router, "user@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created runtimeWithTokenResponse
	json.NewDecoder(rec.Body).Decode(&created)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+created.ID+"/reactivate", nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reactivate: status = %d, want 403", rec.Code)
	}
}

func TestRuntimeHTTP_Delete(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := auth.CreateUser(ctx, db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "")
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	adminCookie := loginAs(t, router, "admin@example.com", "password123")
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	created := func(name string) runtimeWithTokenResponse {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d", name, rec.Code)
		}
		var created runtimeWithTokenResponse
		json.NewDecoder(rec.Body).Decode(&created)
		return created
	}

	empty := created("empty")

	// A runtime with a project cannot be deleted (400, names the block).
	withProject := created("withproject")
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		 SELECT 'p', r.id, '/', '/bin/bash', '{}', u.id
		 FROM runtimes r CROSS JOIN users u
		 WHERE r.id = $1 AND u.email = 'admin@example.com'`, withProject.ID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/runtimes/"+withProject.ID, nil)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete with project: status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}

	// Non-admin is rejected with 403.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/runtimes/"+empty.ID, nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete: status = %d, want 403", rec.Code)
	}

	// Unknown runtime is a 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/runtimes/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: status = %d, want 404", rec.Code)
	}

	// Deleting an empty (even revoked) runtime works; the record is gone.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+empty.ID+"/revoke", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/runtimes/"+empty.ID, nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete empty: status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var list []runtimeResponse
	json.NewDecoder(rec.Body).Decode(&list)
	for _, rt := range list {
		if rt.ID == empty.ID {
			t.Fatalf("deleted runtime still listed: %+v", rt)
		}
	}

	// Audit trail records the deletion.
	events, _, err := audit.List(ctx, db.Pool, audit.Filter{EventType: "runtime.deleted"})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit runtime.deleted: %v, %d events", err, len(events))
	}
}

func TestRuntimeHTTP_DeleteDropsLiveConnection(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "")
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	adminCookie := loginAs(t, router, "admin@example.com", "password123")

	// Register a runtime and connect a fake agent to it.
	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created runtimeWithTokenResponse
	json.NewDecoder(rec.Body).Decode(&created)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startFakeRuntime(t, runCtx, server.URL, created.Token, "test-user", t.TempDir(), []string{"/bin/bash"})
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

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/runtimes/"+created.ID, nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete online runtime: status = %d", rec.Code)
	}

	// The live connection is dropped: the metrics gauge goes to zero.
	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return strings.Contains(rec.Body.String(), `"runtime_connections": 0`) ||
			strings.Contains(rec.Body.String(), `"runtime_connections":0`)
	})
}
