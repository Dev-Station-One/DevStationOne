package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/turnstile"
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	// Wrong password: unauthorized, no cookie.
	body, _ := json.Marshal(map[string]string{"email": "flow@example.com", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401", rec.Code)
	}

	// Correct password: completes mandatory 2FA enrollment (fresh account,
	// docs/DECISIONS.md ADR-047) and ends with a session cookie.
	sessionCookie := loginAs(t, router, "flow@example.com", "password123")
	if sessionCookie.Name != auth.CookieName {
		t.Fatalf("expected a %s cookie, got %q", auth.CookieName, sessionCookie.Name)
	}

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

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	// Every account starts unenrolled (docs/DECISIONS.md ADR-047), so the
	// session cookie is actually set by /2fa/totp/confirm, not /auth/login.
	// setSessionCookie/requestIsSecure don't care which endpoint calls them,
	// so exercising the flag once through enrollment covers every caller —
	// each mutate variant gets a fresh account since an account can only
	// enroll once.
	n := 0
	cookieFrom := func(mutate func(*http.Request)) *http.Cookie {
		n++
		email := fmt.Sprintf("cookie-scheme-%d@example.com", n)
		if _, err := auth.CreateUser(ctx, db.Pool, email, "password123", false); err != nil {
			t.Fatalf("create user: %v", err)
		}

		body, _ := json.Marshal(map[string]string{"email": email, "password": "password123"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		mutate(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var challenge struct {
			ChallengeToken string `json:"challenge_token"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
			t.Fatalf("decode login response: %v", err)
		}
		return completeTOTPEnrollment(t, router, mutate, email, challenge.ChallengeToken)
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
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

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"editor_font":"fira-code","font_size":16}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put editor font prefs: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u = get()
	if u.EditorFont != "fira-code" || u.FontSize != 16 {
		t.Fatalf("editor prefs after put = %+v", u)
	}
	if u.TerminalFont != "jetbrainsmono-nerd" {
		t.Fatalf("terminal_font = %q, want unchanged", u.TerminalFont)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"editor_font":"comic-sans"}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid editor_font: status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"font_size":11}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("font_size 11: status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"font_size":19}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("font_size 19: status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/preferences", bytes.NewReader([]byte(`{"terminal_font":"cascadia-code"}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put extended terminal font: status = %d", rec.Code)
	}
	if got := get().TerminalFont; got != "cascadia-code" {
		t.Fatalf("terminal_font after put = %q, want cascadia-code", got)
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

func TestTurnstileConfigHandler(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	get := func(router http.Handler) turnstileConfigResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/turnstile-config", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var resp turnstileConfigResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	disabled, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
	if resp := get(disabled); resp.Enabled || resp.SiteKey != "" {
		t.Fatalf("unconfigured: %+v, want disabled/empty", resp)
	}

	enabled, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "sitekey123", "secretkey456", TwoFactorConfig{})
	if resp := get(enabled); !resp.Enabled || resp.SiteKey != "sitekey123" {
		t.Fatalf("configured: %+v, want enabled with site key", resp)
	}
}

func TestLogin_TurnstileEnabled(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "turnstile@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		json.NewEncoder(w).Encode(map[string]any{"success": r.FormValue("response") == "valid-token"})
	}))
	t.Cleanup(server.Close)
	origURL := turnstile.SiteVerifyURL
	turnstile.SiteVerifyURL = server.URL
	t.Cleanup(func() { turnstile.SiteVerifyURL = origURL })

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "sitekey", "secretkey", TwoFactorConfig{})

	login := func(turnstileToken string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{
			"email": "turnstile@example.com", "password": "password123", "turnstile_token": turnstileToken,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Missing/invalid token: rejected before password is even checked, no
	// session cookie, and the rejection is audited distinctly from a bad
	// password.
	if rec := login(""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token: status = %d, want 400", rec.Code)
	}
	if rec := login("wrong-token"); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong token: status = %d, want 400", rec.Code)
	}
	events, _, err := audit.List(ctx, db.Pool, audit.Filter{EventType: audit.EventLoginTurnstileFailed})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit EventLoginTurnstileFailed: %v, %d events, want 2", err, len(events))
	}

	// Valid token, correct password: succeeds exactly like the no-Turnstile
	// flow — a fresh account isn't 2FA-enrolled yet, so this is the
	// enrollment challenge rather than a session cookie directly.
	rec := login("valid-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var challenge struct {
		RequiresEnrollment bool `json:"requires_enrollment"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !challenge.RequiresEnrollment {
		t.Fatalf("valid token: expected requires_enrollment for a fresh account")
	}
}
