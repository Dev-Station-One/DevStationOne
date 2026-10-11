package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/mail"
)

// sessionUserFromRequest resolves the caller's session cookie directly.
// The dual-subject 2FA handlers are registered without requireSession (they
// also serve the login-gate path, where no session exists yet), so
// requireSession never populated the context for calls from account
// settings (adding a factor while signed in).
func sessionUserFromRequest(r *http.Request, db *database.DB) (auth.User, bool) {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return auth.User{}, false
	}
	_, user, err := auth.SessionAndUser(r.Context(), db.Pool, cookie.Value)
	if err != nil {
		return auth.User{}, false
	}
	return user, true
}

// resolveTwoFactorSubject returns the user a 2FA setup/enrollment request
// applies to: the authenticated session if one exists, otherwise the
// pending login identified by challengeToken. The mandatory-enrollment gate
// (docs/DECISIONS.md ADR-047) calls the very same setup/confirm handlers a
// logged-in user calls from account settings to add a second factor — the
// only difference is which of these two a request arrives with.
//
// The challenge path only accepts PurposeEnroll challenges (ADR-050): the
// login handler issues one solely when the account has no confirmed factor,
// so a verification-stage token can never reach enrollment — the attacker
// with the victim's password cannot re-enroll their own authenticator over
// the victim's 2FA or mint a session.
func resolveTwoFactorSubject(r *http.Request, db *database.DB, challengeToken string) (auth.User, bool) {
	if user, ok := userFromContext(r.Context()); ok {
		return user, true
	}
	if user, ok := sessionUserFromRequest(r, db); ok {
		return user, true
	}
	challenge, ok := auth.GetChallenge(challengeToken)
	if !ok || challenge.Purpose != auth.PurposeEnroll {
		return auth.User{}, false
	}
	user, err := auth.GetUserByID(r.Context(), db.Pool, challenge.UserID)
	if err != nil {
		return auth.User{}, false
	}
	return user, true
}

// auditFactorAdded logs EventTwoFactorEnrolled the first time an account
// crosses from unenrolled to enrolled, and EventTwoFactorFactorAdded for
// every later factor added on top.
func auditFactorAdded(r *http.Request, db *database.DB, user auth.User, method string, wasEnrolled bool) {
	nowEnrolled, _ := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID)
	event := audit.EventTwoFactorFactorAdded
	if !wasEnrolled && nowEnrolled {
		event = audit.EventTwoFactorEnrolled
	}
	audit.Record(r.Context(), db.Pool, event, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": method}, clientIP(r))
}

type factorAddedResponse struct {
	User        *userResponse `json:"user,omitempty"`
	BackupCodes []string      `json:"backup_codes,omitempty"`
}

// finishFactorSetup completes an add-factor call. If the caller already had
// a session (adding a factor from account settings), it just returns the
// result. If not (the mandatory-enrollment gate, driven by challengeToken),
// confirming this factor is also what completes the login, so it creates
// the session here and folds the user into the same response.
func finishFactorSetup(w http.ResponseWriter, r *http.Request, db *database.DB, user auth.User, method string, wasEnrolled bool, challengeToken string, backupCodes []string) {
	auditFactorAdded(r, db, user, method, wasEnrolled)
	auth.ConsumeChallenge(challengeToken)

	resp := factorAddedResponse{BackupCodes: backupCodes}
	if _, hadSession := userFromContext(r.Context()); hadSession {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if _, hadSession := sessionUserFromRequest(r, db); hadSession {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	token, err := auth.CreateSession(r.Context(), db.Pool, user.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	setSessionCookie(w, r, token)
	audit.Record(r.Context(), db.Pool, audit.EventLoginSucceeded, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
	u := toUserResponse(user)
	resp.User = &u
	writeJSON(w, http.StatusOK, resp)
}

// --- methods / status -------------------------------------------------

type webauthnCredentialResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	ResidentKey bool       `json:"resident_key"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

type twoFactorMethodsResponse struct {
	Enrolled             bool                         `json:"enrolled"`
	TOTPConfirmed        bool                         `json:"totp_confirmed"`
	EmailEnabled         bool                         `json:"email_enabled"`
	WebAuthnCredentials  []webauthnCredentialResponse `json:"webauthn_credentials"`
	BackupCodesRemaining int                          `json:"backup_codes_remaining"`
}

func twoFactorMethodsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		m, err := auth.GetTwoFactorMethods(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load two-factor methods"})
			return
		}
		creds := make([]webauthnCredentialResponse, len(m.WebAuthnCredentials))
		for i, c := range m.WebAuthnCredentials {
			creds[i] = webauthnCredentialResponse{ID: c.ID, Name: c.Name, ResidentKey: c.ResidentKey, CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt}
		}
		writeJSON(w, http.StatusOK, twoFactorMethodsResponse{
			Enrolled: m.Enrolled, TOTPConfirmed: m.TOTPConfirmed, EmailEnabled: m.EmailEnabled,
			WebAuthnCredentials: creds, BackupCodesRemaining: m.BackupCodesRemaining,
		})
	}
}

// --- TOTP ---------------------------------------------------------------

func totpSetupHandler(db *database.DB, dbEncryptionKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeToken string `json:"challenge_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		user, ok := resolveTwoFactorSubject(r, db, req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		url, err := auth.SetupTOTPForUser(r.Context(), db.Pool, user.ID, user.Email, dbEncryptionKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set up authenticator app"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"otpauth_url": url})
	}
}

func totpConfirmHandler(db *database.DB, dbEncryptionKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code           string `json:"code"`
			ChallengeToken string `json:"challenge_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		user, ok := resolveTwoFactorSubject(r, db, req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		wasEnrolled, _ := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID)

		switch err := auth.ConfirmTOTPForUser(r.Context(), db.Pool, user.ID, dbEncryptionKey, req.Code); {
		case errors.Is(err, auth.ErrInvalidCode):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
			return
		case errors.Is(err, auth.ErrNotFound):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "call /2fa/totp/setup first"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to confirm authenticator app"})
			return
		}

		codes, err := auth.GenerateAndStoreBackupCodes(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate backup codes"})
			return
		}
		finishFactorSetup(w, r, db, user, "totp", wasEnrolled, req.ChallengeToken, codes)
	}
}

func totpDeleteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		switch err := auth.DeleteTOTP(r.Context(), db.Pool, user.ID); {
		case errors.Is(err, auth.ErrLastFactor):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove your last two-factor method"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove authenticator app"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventTwoFactorFactorRemoved, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": "totp"}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- Email OTP ------------------------------------------------------------

func emailEnableHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeToken string `json:"challenge_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		user, ok := resolveTwoFactorSubject(r, db, req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		wasEnrolled, _ := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID)

		if err := auth.EnableEmailTwoFactor(r.Context(), db.Pool, user.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to enable email codes"})
			return
		}

		var codes []string
		if remaining, _ := auth.CountUnusedBackupCodes(r.Context(), db.Pool, user.ID); remaining == 0 {
			var err error
			if codes, err = auth.GenerateAndStoreBackupCodes(r.Context(), db.Pool, user.ID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate backup codes"})
				return
			}
		}
		finishFactorSetup(w, r, db, user, "email", wasEnrolled, req.ChallengeToken, codes)
	}
}

func emailDisableHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		switch err := auth.DisableEmailTwoFactor(r.Context(), db.Pool, user.ID); {
		case errors.Is(err, auth.ErrLastFactor):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove your last two-factor method"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to disable email codes"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventTwoFactorFactorRemoved, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": "email"}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

func emailRequestCodeHandler(db *database.DB, mailCfg mail.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeToken string `json:"challenge_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		challenge, ok := auth.GetChallenge(req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login challenge expired or invalid"})
			return
		}
		user, err := auth.GetUserByID(r.Context(), db.Pool, challenge.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send code"})
			return
		}
		if enabled, err := auth.IsEmailTwoFactorEnabled(r.Context(), db.Pool, user.ID); err != nil || !enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email codes are not enabled for this account"})
			return
		}
		if !mailCfg.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "email delivery is not configured"})
			return
		}
		code, err := auth.GenerateEmailOTP()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send code"})
			return
		}
		auth.SetChallengeEmailCode(req.ChallengeToken, code)
		body := fmt.Sprintf("Your DevStationOne verification code is: %s\n\nIt expires in 5 minutes.", code)
		if err := mail.Send(mailCfg, user.Email, "Your DevStationOne verification code", body); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send code"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- backup codes -----------------------------------------------------

func backupCodesRegenerateHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		codes, err := auth.GenerateAndStoreBackupCodes(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate backup codes"})
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"backup_codes": codes})
	}
}

// --- login step-up verification ----------------------------------------

type twoFactorVerifyRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Method         string `json:"method"`
	Code           string `json:"code"`
	TrustDevice    bool   `json:"trust_device"`
}

func twoFactorVerifyHandler(db *database.DB, dbEncryptionKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req twoFactorVerifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		challenge, ok := auth.GetChallenge(req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login challenge expired or invalid"})
			return
		}
		user, err := auth.GetUserByID(r.Context(), db.Pool, challenge.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed"})
			return
		}

		var verified bool
		switch req.Method {
		case "totp":
			verified, err = auth.VerifyTOTPForUser(r.Context(), db.Pool, user.ID, dbEncryptionKey, req.Code)
		case "email":
			verified = req.Code != "" && challenge.EmailCode != "" &&
				subtle.ConstantTimeCompare([]byte(req.Code), []byte(challenge.EmailCode)) == 1
		case "backup_code":
			verified, err = auth.ConsumeBackupCode(r.Context(), db.Pool, user.ID, req.Code)
			if verified {
				audit.Record(r.Context(), db.Pool, audit.EventBackupCodeUsed, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
			}
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown method"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed"})
			return
		}
		if !verified {
			auth.RecordChallengeFailure(req.ChallengeToken)
			audit.Record(r.Context(), db.Pool, audit.EventTwoFactorVerifyFailed, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": req.Method}, clientIP(r))
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
			return
		}

		auth.ConsumeChallenge(req.ChallengeToken)
		completeLogin(w, r, db, user, audit.EventLoginSucceeded, req.TrustDevice)
	}
}

// --- WebAuthn: registration (passkey enrollment / 2FA-as-passkey add) ---

func webauthnRegisterBeginHandler(db *database.DB, wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeToken string `json:"challenge_token"`
			ResidentKey    bool   `json:"resident_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		user, ok := resolveTwoFactorSubject(r, db, req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		waUser, err := auth.LoadWebAuthnUser(r.Context(), db.Pool, user)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey registration"})
			return
		}

		rk := protocol.ResidentKeyRequirementDiscouraged
		if req.ResidentKey {
			rk = protocol.ResidentKeyRequirementRequired
		}
		creation, sessionData, err := wa.BeginRegistration(waUser, webauthnlib.WithResidentKeyRequirement(rk))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey registration"})
			return
		}

		token := req.ChallengeToken
		if _, hadSession := userFromContext(r.Context()); hadSession {
			// Adding another passkey from account settings: no login in
			// progress to reuse, mint a throwaway token to hold ceremony
			// state between begin and finish.
			if token, err = auth.NewChallenge(user.ID, false, auth.PurposeEnroll); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey registration"})
				return
			}
		} else if user2, ok := sessionUserFromRequest(r, db); ok {
			if token, err = auth.NewChallenge(user2.ID, false, auth.PurposeEnroll); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey registration"})
				return
			}
		}
		auth.SetChallengeWebAuthnData(token, sessionData)
		writeJSON(w, http.StatusOK, map[string]any{"challenge_token": token, "options": creation})
	}
}

func webauthnRegisterFinishHandler(db *database.DB, wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("challenge_token")
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "Passkey"
		}
		residentKey := r.URL.Query().Get("resident_key") == "true"

		user, ok := resolveTwoFactorSubject(r, db, token)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		challenge, ok := auth.GetChallenge(token)
		if !ok || challenge.WebAuthnData == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "registration challenge expired or invalid"})
			return
		}
		wasEnrolled, _ := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID)

		waUser, err := auth.LoadWebAuthnUser(r.Context(), db.Pool, user)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "passkey registration failed"})
			return
		}
		cred, err := wa.FinishRegistration(waUser, *challenge.WebAuthnData, r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "passkey registration failed"})
			return
		}
		if err := auth.StoreWebAuthnCredential(r.Context(), db.Pool, user.ID, name, residentKey, cred); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save passkey"})
			return
		}
		finishFactorSetup(w, r, db, user, "webauthn", wasEnrolled, token, nil)
	}
}

func webauthnCredentialDeleteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		switch err := auth.DeleteWebAuthnCredential(r.Context(), db.Pool, user.ID, r.PathValue("credentialID")); {
		case errors.Is(err, auth.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "passkey not found"})
			return
		case errors.Is(err, auth.ErrLastFactor):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove your last two-factor method"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove passkey"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventTwoFactorFactorRemoved, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": "webauthn"}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- WebAuthn: 2FA-as-second-factor login (user known via challenge) ---

func webauthnLoginBeginHandler(db *database.DB, wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeToken string `json:"challenge_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		challenge, ok := auth.GetChallenge(req.ChallengeToken)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login challenge expired or invalid"})
			return
		}
		user, err := auth.GetUserByID(r.Context(), db.Pool, challenge.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey verification"})
			return
		}
		waUser, err := auth.LoadWebAuthnUser(r.Context(), db.Pool, user)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey verification"})
			return
		}
		assertion, sessionData, err := wa.BeginLogin(waUser)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey verification"})
			return
		}
		auth.SetChallengeWebAuthnData(req.ChallengeToken, sessionData)
		writeJSON(w, http.StatusOK, map[string]any{"options": assertion})
	}
}

func webauthnLoginFinishHandler(db *database.DB, wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("challenge_token")
		trustDevice := r.URL.Query().Get("trust_device") == "true"

		challenge, ok := auth.GetChallenge(token)
		if !ok || challenge.WebAuthnData == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login challenge expired or invalid"})
			return
		}
		user, err := auth.GetUserByID(r.Context(), db.Pool, challenge.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed"})
			return
		}
		waUser, err := auth.LoadWebAuthnUser(r.Context(), db.Pool, user)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed"})
			return
		}
		cred, err := wa.FinishLogin(waUser, *challenge.WebAuthnData, r)
		if err != nil {
			audit.Record(r.Context(), db.Pool, audit.EventTwoFactorVerifyFailed, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, map[string]any{"method": "webauthn"}, clientIP(r))
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "passkey verification failed"})
			return
		}
		_ = auth.TouchWebAuthnCredential(r.Context(), db.Pool, cred)
		auth.ConsumeChallenge(token)
		completeLogin(w, r, db, user, audit.EventLoginSucceeded, trustDevice)
	}
}

// --- WebAuthn: full passwordless login ----------------------------------

func webauthnPasswordlessBeginHandler(wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assertion, sessionData, err := wa.BeginDiscoverableLogin()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey login"})
			return
		}
		token, err := auth.NewChallenge("", false, auth.PurposeVerify)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start passkey login"})
			return
		}
		auth.SetChallengeWebAuthnData(token, sessionData)
		writeJSON(w, http.StatusOK, map[string]any{"challenge_token": token, "options": assertion})
	}
}

func webauthnPasswordlessFinishHandler(db *database.DB, wa *webauthnlib.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("challenge_token")
		trustDevice := r.URL.Query().Get("trust_device") == "true"

		challenge, ok := auth.GetChallenge(token)
		if !ok || challenge.WebAuthnData == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login challenge expired or invalid"})
			return
		}

		// A successful passkey login is itself possession-plus-verification
		// (biometric/PIN unlock), so it satisfies the mandatory-2FA
		// requirement outright — no separate step-up factor is asked for.
		validatedUser, cred, err := wa.FinishPasskeyLogin(auth.LoadWebAuthnUserByHandle(r.Context(), db.Pool), *challenge.WebAuthnData, r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "passkey login failed"})
			return
		}
		wu, ok := validatedUser.(auth.WebAuthnUser)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "passkey login failed"})
			return
		}
		_ = auth.TouchWebAuthnCredential(r.Context(), db.Pool, cred)
		auth.ConsumeChallenge(token)
		completeLogin(w, r, db, wu.User, audit.EventPasskeyLoginSucceeded, trustDevice)
	}
}

// --- trusted devices ------------------------------------------------------

type trustedDeviceResponse struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func trustedDevicesListHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		devices, err := auth.ListTrustedDevices(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list trusted devices"})
			return
		}
		resp := make([]trustedDeviceResponse, 0, len(devices))
		for _, d := range devices {
			resp = append(resp, trustedDeviceResponse{ID: d.ID, UserAgent: d.UserAgent, IPAddress: d.IPAddress, CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func trustedDeviceDeleteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		ok, err := auth.DeleteTrustedDevice(r.Context(), db.Pool, user.ID, r.PathValue("deviceID"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke trusted device"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trusted device not found"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventTrustedDeviceRevoked, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- recovery ------------------------------------------------------------

func recoveryRequestHandler(db *database.DB, mailCfg mail.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Always 202 regardless of outcome: whether the account exists or
		// has 2FA enrolled must never be observable from this response.
		if user, err := auth.GetUserByEmail(r.Context(), db.Pool, req.Email); err == nil {
			if enrolled, err := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID); err == nil && enrolled && mailCfg.Enabled() {
				if token, err := auth.CreateRecoveryToken(r.Context(), db.Pool, user.ID); err == nil {
					body := fmt.Sprintf("Use this code to regain access to your account and set up two-factor authentication again:\n\n%s\n\nThis code expires in 30 minutes. If you didn't request this, ignore this email.", token)
					_ = mail.Send(mailCfg, user.Email, "DevStationOne account recovery", body)
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func recoveryConfirmHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		userID, err := auth.ConsumeRecoveryToken(r.Context(), db.Pool, req.Token)
		if errors.Is(err, auth.ErrNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "recovery link is invalid or expired"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "recovery failed"})
			return
		}
		user, err := auth.GetUserByID(r.Context(), db.Pool, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "recovery failed"})
			return
		}
		if err := auth.ClearAllTwoFactor(r.Context(), db.Pool, userID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "recovery failed"})
			return
		}
		completeLogin(w, r, db, user, audit.EventTwoFactorRecoveryUsed, false)
	}
}

// --- admin reset -----------------------------------------------------------

func adminResetTwoFactorHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r.Context())
		target, err := auth.GetUserByID(r.Context(), db.Pool, r.PathValue("userID"))
		if errors.Is(err, auth.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up user"})
			return
		}
		if err := auth.ClearAllTwoFactor(r.Context(), db.Pool, target.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reset two-factor"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventTwoFactorAdminReset, audit.Actor{UserID: admin.ID, Email: admin.Email}, "user", target.ID, map[string]any{"email": target.Email}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}
