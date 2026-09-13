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

	"devstation/internal/auth"
	"devstation/internal/database"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	url := os.Getenv("DEVSTATION_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATION_DATABASE_URL not set; skipping integration test")
	}
	db, err := database.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), "TRUNCATE sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestLoginLogoutFlow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "flow@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router := NewRouter(db, logger, t.TempDir())

	// Wrong password: unauthorized, no cookie.
	body, _ := json.Marshal(map[string]string{"email": "flow@example.com", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401", rec.Code)
	}

	// Correct password: sets session cookie.
	body, _ = json.Marshal(map[string]string{"email": "flow@example.com", "password": "password123"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.CookieName {
		t.Fatalf("expected one %s cookie, got %v", auth.CookieName, cookies)
	}
	sessionCookie := cookies[0]

	// Protected endpoint without cookie: unauthorized.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: status = %d, want 401", rec.Code)
	}

	// Protected endpoint with cookie: OK.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with cookie: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var user userResponse
	if err := json.NewDecoder(rec.Body).Decode(&user); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if user.Email != "flow@example.com" {
		t.Errorf("Email = %q, want flow@example.com", user.Email)
	}

	// Logout, then the same cookie must no longer authenticate.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: status = %d, want 401", rec.Code)
	}
}
