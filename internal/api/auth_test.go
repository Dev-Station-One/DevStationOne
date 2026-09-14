package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/auth"
	"devstationone/internal/database"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	url := os.Getenv("DEVSTATIONONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATIONONE_TEST_DATABASE_URL not set; skipping integration test")
	}
	if err := database.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := database.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), "TRUNCATE projects, runtimes, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), "UPDATE audit_settings SET retention_days = 90 WHERE id = 1"); err != nil {
		t.Fatalf("reset audit_settings: %v", err)
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
	router, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)

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

func TestSessionCookieSecureFlagFollowsRequestScheme(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "cookie-scheme@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	body, _ := json.Marshal(map[string]string{"email": "cookie-scheme@example.com", "password": "password123"})

	cookieFrom := func(mutate func(*http.Request)) *http.Cookie {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		mutate(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.CookieName {
				return c
			}
		}
		t.Fatalf("no %s cookie in response", auth.CookieName)
		return nil
	}

	// Plain HTTP (LAN IP over http://): Secure cookie would be dropped by the
	// browser, so the flag must be off.
	if c := cookieFrom(func(r *http.Request) {}); c.Secure {
		t.Error("plain http: cookie Secure = true, want false")
	}

	// Direct TLS: Secure on.
	if c := cookieFrom(func(r *http.Request) { r.TLS = &tls.ConnectionState{} }); !c.Secure {
		t.Error("https: cookie Secure = false, want true")
	}

	// Behind a TLS-terminating proxy: Secure on.
	if c := cookieFrom(func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }); !c.Secure {
		t.Error("X-Forwarded-Proto https: cookie Secure = false, want true")
	}
}

func TestTerminalFontPreference(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "fontpref@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	cookie := loginAs(t, router, "fontpref@example.com", "password123")

	get := func() userResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("session: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var u userResponse
		if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
			t.Fatalf("decode session: %v", err)
		}
		return u
	}

	if got := get().TerminalFont; got != "system" {
		t.Fatalf("default terminal_font = %q, want system", got)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"terminal_font":"jetbrainsmono-nerd"}`)))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put preferences: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := get().TerminalFont; got != "jetbrainsmono-nerd" {
		t.Fatalf("terminal_font after put = %q, want jetbrainsmono-nerd", got)
	}

	// Unknown values are rejected and change nothing.
	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"terminal_font":"comic-sans"}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid font: status = %d, want 400", rec.Code)
	}
	if got := get().TerminalFont; got != "jetbrainsmono-nerd" {
		t.Fatalf("terminal_font after rejected put = %q", got)
	}

	// Theme: default dark, partial update touches only the named field.
	if got := get().Theme; got != "dark" {
		t.Fatalf("default theme = %q, want dark", got)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"theme":"light"}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put theme: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u := get()
	if u.Theme != "light" || u.TerminalFont != "jetbrainsmono-nerd" {
		t.Fatalf("after theme put: theme = %q, terminal_font = %q (partial update must not touch the other field)", u.Theme, u.TerminalFont)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"theme":"solarized"}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid theme: status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty put: status = %d, want 400", rec.Code)
	}

	// Per-user: another user's preference is untouched.
	if _, err := auth.CreateUser(ctx, db.Pool, "otherfont@example.com", "password123", false); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	other := loginAs(t, router, "otherfont@example.com", "password123")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(other)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var otherUser userResponse
	json.NewDecoder(rec.Body).Decode(&otherUser)
	if otherUser.TerminalFont != "system" {
		t.Fatalf("other user terminal_font = %q, want system", otherUser.TerminalFont)
	}
}
