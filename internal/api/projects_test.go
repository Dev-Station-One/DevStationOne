package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/auth"
)

func createRuntimeAs(t *testing.T, router http.Handler, cookie *http.Cookie, name string) string {
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
	return created.ID
}

func projectBody(runtimeID string) []byte {
	body, _ := json.Marshal(projectRequest{
		Name:             "My Application",
		RuntimeID:        runtimeID,
		OSUser:           "brian",
		WorkingDirectory: "/home/brian/src/my-application",
		Shell:            "/bin/bash",
		Environment:      map[string]string{"NODE_ENV": "development"},
	})
	return body
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
	router := NewRouter(db, logger, t.TempDir())

	adminCookie := loginAs(t, router, "admin@example.com", "password123")
	aliceCookie := loginAs(t, router, "alice@example.com", "password123")
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	runtimeID := createRuntimeAs(t, router, adminCookie, "dev01")

	// Alice creates a project.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody(runtimeID)))
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

	// Alice sees her own project.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(aliceCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var aliceList []projectResponse
	json.NewDecoder(rec.Body).Decode(&aliceList)
	if len(aliceList) != 1 || aliceList[0].ID != created.ID {
		t.Fatalf("alice list = %+v, want her own project", aliceList)
	}

	// Bob does not see Alice's project.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var bobList []projectResponse
	json.NewDecoder(rec.Body).Decode(&bobList)
	if len(bobList) != 0 {
		t.Fatalf("bob list = %+v, want no projects", bobList)
	}

	// Bob cannot fetch Alice's project directly; it looks not-found, not forbidden.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob get alice's project: status = %d, want 404", rec.Code)
	}

	// Bob cannot delete Alice's project either.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+created.ID, nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete alice's project: status = %d, want 404", rec.Code)
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
	router := NewRouter(db, logger, t.TempDir())
	adminCookie := loginAs(t, router, "admin@example.com", "password123")
	runtimeID := createRuntimeAs(t, router, adminCookie, "dev01")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody(runtimeID)))
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
	if patched.Name != "Renamed" || patched.OSUser != created.OSUser {
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
	router := NewRouter(db, logger, t.TempDir())
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
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projectBody("00000000-0000-0000-0000-000000000000")))
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
