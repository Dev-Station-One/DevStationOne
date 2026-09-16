package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
)

// totpSecretsByEmail remembers the TOTP secret each test account enrolled
// with, so a second loginAs call for the same (still-enrolled) account can
// complete the ordinary 2FA step-up instead of the one-time enrollment.
// Process-lifetime map: each test truncates the database, not this map, but
// every enrollment overwrites its own email's entry before use, so a stale
// value is never read.
var (
	totpSecretsMu      sync.Mutex
	totpSecretsByEmail = map[string]string{}
)

// loginAs logs email in and returns its session cookie. Every account
// starts unenrolled in mandatory 2FA (docs/DECISIONS.md ADR-047), so a
// fresh login always comes back with requires_enrollment; loginAs drives
// that gate closed with TOTP (the simplest factor to automate) rather than
// making every one of this helper's ~20 callers do it by hand.
func loginAs(t *testing.T, router http.Handler, email, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: status = %d, body = %s", email, rec.Code, rec.Body.String())
	}

	var challenge struct {
		RequiresEnrollment bool   `json:"requires_enrollment"`
		RequiresTwoFactor  bool   `json:"requires_2fa"`
		ChallengeToken     string `json:"challenge_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("login %s: decode response: %v", email, err)
	}

	switch {
	case challenge.RequiresEnrollment:
		return completeTOTPEnrollment(t, router, func(r *http.Request) {}, email, challenge.ChallengeToken)
	case challenge.RequiresTwoFactor:
		return completeTOTPVerify(t, router, email, challenge.ChallengeToken)
	default:
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("login %s: expected 1 cookie, got %d", email, len(cookies))
		}
		return cookies[0]
	}
}

// completeTOTPEnrollment drives the mandatory-2FA enrollment gate closed
// with a freshly generated TOTP secret, returning the session cookie the
// confirm call sets. mutate is applied to both the setup and confirm
// requests, letting callers exercise request-scheme-dependent behavior
// (e.g. Secure cookie flag) through the enrollment path.
func completeTOTPEnrollment(t *testing.T, router http.Handler, mutate func(*http.Request), email, challengeToken string) *http.Cookie {
	t.Helper()

	setupBody, _ := json.Marshal(map[string]string{"challenge_token": challengeToken})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/totp/setup", bytes.NewReader(setupBody))
	mutate(req)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp setup %s: status = %d, body = %s", email, rec.Code, rec.Body.String())
	}
	var setup struct {
		OTPAuthURL string `json:"otpauth_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&setup); err != nil {
		t.Fatalf("decode totp setup %s: %v", email, err)
	}
	parsed, err := url.Parse(setup.OTPAuthURL)
	if err != nil {
		t.Fatalf("parse otpauth url %s: %v", email, err)
	}
	secret := parsed.Query().Get("secret")
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code %s: %v", email, err)
	}

	confirmBody, _ := json.Marshal(map[string]string{"challenge_token": challengeToken, "code": code})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/totp/confirm", bytes.NewReader(confirmBody))
	mutate(req)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp confirm %s: status = %d, body = %s", email, rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("totp confirm %s: expected 1 cookie, got %d", email, len(cookies))
	}

	totpSecretsMu.Lock()
	totpSecretsByEmail[email] = secret
	totpSecretsMu.Unlock()
	return cookies[0]
}

// completeTOTPVerify completes the ordinary 2FA step-up (as opposed to
// completeTOTPEnrollment's one-time confirm) for an account loginAs already
// enrolled earlier in this test binary's lifetime.
func completeTOTPVerify(t *testing.T, router http.Handler, email, challengeToken string) *http.Cookie {
	t.Helper()
	totpSecretsMu.Lock()
	secret, ok := totpSecretsByEmail[email]
	totpSecretsMu.Unlock()
	if !ok {
		t.Fatalf("login %s: account is 2FA-enrolled but loginAs never recorded its TOTP secret (enrolled via a different path?)", email)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code %s: %v", email, err)
	}
	body, _ := json.Marshal(map[string]any{"challenge_token": challengeToken, "method": "totp", "code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa verify %s: status = %d, body = %s", email, rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("2fa verify %s: expected 1 cookie, got %d", email, len(cookies))
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
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
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
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
