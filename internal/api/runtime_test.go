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
	router := NewRouter(db, logger, t.TempDir())

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
	router := NewRouter(db, logger, t.TempDir())
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
}
