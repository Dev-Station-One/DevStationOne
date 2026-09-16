package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
)

// enrollTOTP creates userEmail, logs in, and completes TOTP enrollment,
// returning the session cookie and the raw TOTP secret (so the caller can
// generate further valid codes for the 2FA-verify step-up path).
func enrollTOTP(t *testing.T, router http.Handler, email, password string) (*http.Cookie, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var challenge struct {
		RequiresEnrollment bool   `json:"requires_enrollment"`
		ChallengeToken     string `json:"challenge_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !challenge.RequiresEnrollment {
		t.Fatalf("expected requires_enrollment for a fresh account")
	}

	setupBody, _ := json.Marshal(map[string]string{"challenge_token": challenge.ChallengeToken})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/totp/setup", bytes.NewReader(setupBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp setup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		OTPAuthURL string `json:"otpauth_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&setup); err != nil {
		t.Fatalf("decode totp setup: %v", err)
	}
	parsed, err := url.Parse(setup.OTPAuthURL)
	if err != nil {
		t.Fatalf("parse otpauth url: %v", err)
	}
	secret := parsed.Query().Get("secret")
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	confirmBody, _ := json.Marshal(map[string]string{"challenge_token": challenge.ChallengeToken, "code": code})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/totp/confirm", bytes.NewReader(confirmBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp confirm: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var confirmResp struct {
		BackupCodes []string `json:"backup_codes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&confirmResp); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if len(confirmResp.BackupCodes) != 10 {
		t.Fatalf("got %d backup codes, want 10", len(confirmResp.BackupCodes))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("totp confirm: expected 1 cookie, got %d", len(cookies))
	}
	return cookies[0], secret
}

func TestTwoFactor_MandatoryEnrollmentAndMethods(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "enroll@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cookie, _ := enrollTOTP(t, router, "enroll@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/2fa/methods", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("methods: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var methods twoFactorMethodsResponse
	if err := json.NewDecoder(rec.Body).Decode(&methods); err != nil {
		t.Fatalf("decode methods: %v", err)
	}
	if !methods.Enrolled || !methods.TOTPConfirmed {
		t.Fatalf("methods = %+v, want enrolled+totp_confirmed", methods)
	}
	if methods.BackupCodesRemaining != 10 {
		t.Fatalf("backup_codes_remaining = %d, want 10", methods.BackupCodesRemaining)
	}

	// Logging in again now hits the ordinary step-up path, not enrollment.
	body, _ := json.Marshal(map[string]string{"email": "enroll@example.com", "password": "password123"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second login: status = %d", rec.Code)
	}
	var second struct {
		RequiresTwoFactor bool `json:"requires_2fa"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&second); err != nil {
		t.Fatalf("decode second login: %v", err)
	}
	if !second.RequiresTwoFactor {
		t.Fatalf("second login: expected requires_2fa for an enrolled account")
	}
}

// TestTwoFactor_AddFactorWhileSignedIn covers the account-settings path of
// the dual-subject endpoints: with a session cookie and no challenge token,
// totp/setup, totp/confirm, email/enable, and webauthn/register/begin must
// resolve the caller from the session (resolveTwoFactorSubject), not 401.
func TestTwoFactor_AddFactorWhileSignedIn(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "addfactor@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cookie, _ := enrollTOTP(t, router, "addfactor@example.com", "password123")

	// Email codes, no challenge token — the request the Account page sends.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/email/enable", bytes.NewReader([]byte("{}")))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("email/enable with session: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// A second TOTP setup must also resolve the session user.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/totp/setup", bytes.NewReader([]byte("{}")))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp/setup with session: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// WebAuthn registration begin likewise (the browser ceremony itself is
	// not exercised here — only that begin resolves the session subject and
	// mints a ceremony token for the subsequent finish call).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/webauthn/register/begin", bytes.NewReader([]byte(`{"resident_key":true}`)))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webauthn/register/begin with session: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var beginResp struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&beginResp); err != nil {
		t.Fatalf("decode webauthn begin: %v", err)
	}
	if beginResp.ChallengeToken == "" {
		t.Fatal("webauthn/register/begin with session returned an empty challenge_token — the finish call could never resolve the ceremony")
	}
}

func TestTwoFactor_LastFactorCannotBeRemoved(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "lastfactor@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cookie, _ := enrollTOTP(t, router, "lastfactor@example.com", "password123")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/2fa/totp", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last factor: status = %d, want 409", rec.Code)
	}
}

func TestTwoFactor_BackupCodeCompletesLogin(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "backupcode@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, _ = enrollTOTP(t, router, "backupcode@example.com", "password123")

	// Fetch a fresh set of backup codes directly (enrollTOTP already
	// consumed the confirm response's set, but never used one).
	codes, err := auth.GenerateAndStoreBackupCodes(t.Context(), db.Pool, mustUserID(t, db, "backupcode@example.com"))
	if err != nil {
		t.Fatalf("generate backup codes: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"email": "backupcode@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var challenge struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	// Wrong code first: rejected, audited, doesn't consume anything.
	verifyBody, _ := json.Marshal(map[string]any{"challenge_token": challenge.ChallengeToken, "method": "backup_code", "code": "WRONGCODE"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify", bytes.NewReader(verifyBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong backup code: status = %d, want 401", rec.Code)
	}

	// Correct code: succeeds.
	verifyBody, _ = json.Marshal(map[string]any{"challenge_token": challenge.ChallengeToken, "method": "backup_code", "code": codes[0]})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify", bytes.NewReader(verifyBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct backup code: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events, _, err := audit.List(t.Context(), db.Pool, audit.Filter{EventType: audit.EventBackupCodeUsed})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit EventBackupCodeUsed: %v, %d events, want 1", err, len(events))
	}

	// Same code again: single-use, must fail now.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify", bytes.NewReader(verifyBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused backup code: status = %d, want 401", rec.Code)
	}
}

func TestTwoFactor_TrustedDeviceSkipsStepUp(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "trusted@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, secret := enrollTOTP(t, router, "trusted@example.com", "password123")

	loginBody, _ := json.Marshal(map[string]string{"email": "trusted@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var challenge struct {
		ChallengeToken string `json:"challenge_token"`
	}
	json.NewDecoder(rec.Body).Decode(&challenge)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	verifyBody, _ := json.Marshal(map[string]any{"challenge_token": challenge.ChallengeToken, "method": "totp", "code": code, "trust_device": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify", bytes.NewReader(verifyBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify with trust_device: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var trustCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.TrustedDeviceCookieName {
			trustCookie = c
		}
	}
	if trustCookie == nil {
		t.Fatalf("expected a %s cookie", auth.TrustedDeviceCookieName)
	}

	// A subsequent login carrying the trusted-device cookie skips 2FA
	// entirely and returns a session directly.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	req.AddCookie(trustCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trusted login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var user userResponse
	if err := json.NewDecoder(rec.Body).Decode(&user); err != nil || user.Email != "trusted@example.com" {
		t.Fatalf("trusted login did not return a session directly: err=%v body decoded as %+v", err, user)
	}

	// Revoking it forces 2FA again.
	sessionCookie := func() *http.Cookie {
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.CookieName {
				return c
			}
		}
		return nil
	}()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/trusted-devices", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var devices []trustedDeviceResponse
	json.NewDecoder(rec.Body).Decode(&devices)
	if len(devices) != 1 {
		t.Fatalf("got %d trusted devices, want 1", len(devices))
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/auth/trusted-devices/"+devices[0].ID, nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke trusted device: status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	req.AddCookie(trustCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var afterRevoke struct {
		RequiresTwoFactor bool `json:"requires_2fa"`
	}
	json.NewDecoder(rec.Body).Decode(&afterRevoke)
	if !afterRevoke.RequiresTwoFactor {
		t.Fatalf("login after revoking trusted device: expected requires_2fa again")
	}
}

func TestTwoFactor_AdminReset(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "admin-reset-admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	target, err := auth.CreateUser(t.Context(), db.Pool, "admin-reset-target@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	adminCookie, _ := enrollTOTP(t, router, "admin-reset-admin@example.com", "password123")
	enrollTOTP(t, router, "admin-reset-target@example.com", "password123")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target.ID+"/2fa/reset", nil)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin reset: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The target must now be routed back through mandatory enrollment.
	body, _ := json.Marshal(map[string]string{"email": "admin-reset-target@example.com", "password": "password123"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var resp struct {
		RequiresEnrollment bool `json:"requires_enrollment"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.RequiresEnrollment {
		t.Fatalf("target after admin reset: expected requires_enrollment")
	}

	events, _, err := audit.List(t.Context(), db.Pool, audit.Filter{EventType: audit.EventTwoFactorAdminReset})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit EventTwoFactorAdminReset: %v, %d events, want 1", err, len(events))
	}
}

func TestTwoFactor_RecoveryConfirmClearsFactors(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	if _, err := auth.CreateUser(t.Context(), db.Pool, "recover@example.com", "password123", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	enrollTOTP(t, router, "recover@example.com", "password123")
	userID := mustUserID(t, db, "recover@example.com")

	// The recovery-request endpoint itself needs SMTP configured to email a
	// link (not exercised here — internal/mail covers header-injection and
	// enable/disable behavior); recovery-confirm's own logic is independent
	// of how the token reached the user, so create one directly.
	token, err := auth.CreateRecoveryToken(t.Context(), db.Pool, userID)
	if err != nil {
		t.Fatalf("create recovery token: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"token": "wrong-token"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/recover/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong recovery token: status = %d, want 401", rec.Code)
	}

	body, _ = json.Marshal(map[string]string{"token": token})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/recover/confirm", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery confirm: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 1 {
		t.Fatalf("recovery confirm: expected a session cookie")
	}

	enrolled, err := auth.IsTwoFactorEnrolled(t.Context(), db.Pool, userID)
	if err != nil || enrolled {
		t.Fatalf("after recovery: enrolled = %v (err %v), want false", enrolled, err)
	}

	// The token is single-use.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/recover/confirm", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery token: status = %d, want 401", rec.Code)
	}
}

func mustUserID(t *testing.T, db *database.DB, email string) string {
	t.Helper()
	u, err := auth.GetUserByEmail(t.Context(), db.Pool, email)
	if err != nil {
		t.Fatalf("look up %s: %v", email, err)
	}
	return u.ID
}
