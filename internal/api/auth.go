package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/turnstile"
)

type loginRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	TurnstileToken string `json:"turnstile_token"`
}

type userResponse struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	DisplayName      string    `json:"display_name"`
	IsAdmin          bool      `json:"is_admin"`
	TerminalFont     string    `json:"terminal_font"`
	Theme            string    `json:"theme"`
	EditorFont       string    `json:"editor_font"`
	FontSize         int       `json:"font_size"`
	EnabledLanguages []string  `json:"enabled_languages"`
	CreatedAt        time.Time `json:"created_at"`
}

func toUserResponse(u auth.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, TerminalFont: u.TerminalFont, Theme: u.Theme, EditorFont: u.EditorFont, FontSize: u.FontSize, EnabledLanguages: u.EnabledLanguages, CreatedAt: u.CreatedAt}
}

// loginChallengeResponse is what /auth/login returns instead of a session
// when the account still needs to complete mandatory 2FA enrollment
// (docs/DECISIONS.md ADR-047) or verify a second factor. challengeToken must
// be presented back to whichever /auth/2fa/... endpoint completes the flow.
type loginChallengeResponse struct {
	RequiresEnrollment bool     `json:"requires_enrollment,omitempty"`
	RequiresTwoFactor  bool     `json:"requires_2fa,omitempty"`
	ChallengeToken     string   `json:"challenge_token"`
	Methods            []string `json:"methods,omitempty"`
}

// completeLogin finishes any login path (password+trusted-device,
// password+2FA-verify, or full passwordless passkey) by creating a session,
// optionally minting a trusted-device cookie, auditing eventType, and
// writing the user response. Shared so every path ends up with identical
// session/cookie/audit behavior.
func completeLogin(w http.ResponseWriter, r *http.Request, db *database.DB, user auth.User, eventType string, trustDevice bool) {
	token, err := auth.CreateSession(r.Context(), db.Pool, user.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	setSessionCookie(w, r, token)

	if trustDevice {
		trustToken, err := auth.CreateTrustedDevice(r.Context(), db.Pool, user.ID, r.UserAgent(), clientIP(r))
		if err == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     auth.TrustedDeviceCookieName,
				Value:    trustToken,
				Path:     "/",
				HttpOnly: true,
				Secure:   requestIsSecure(r),
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(auth.TrustedDeviceTTL.Seconds()),
			})
			audit.Record(r.Context(), db.Pool, audit.EventTrustedDeviceAdded, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
		}
	}

	audit.Record(r.Context(), db.Pool, eventType, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// updatePreferencesHandler PUT /api/v1/auth/preferences — partial update of
// the authenticated user's preferences (terminal font, theme). Fields left
// out are untouched; the request must name at least one.
func updatePreferencesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req struct {
			TerminalFont     *string   `json:"terminal_font"`
			Theme            *string   `json:"theme"`
			DisplayName      *string   `json:"display_name"`
			EditorFont       *string   `json:"editor_font"`
			FontSize         *int      `json:"font_size"`
			EnabledLanguages *[]string `json:"enabled_languages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.TerminalFont == nil && req.Theme == nil && req.DisplayName == nil && req.EditorFont == nil && req.FontSize == nil && req.EnabledLanguages == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to update"})
			return
		}
		if req.TerminalFont != nil && !auth.ValidTerminalFont(*req.TerminalFont) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown terminal_font"})
			return
		}
		if req.EditorFont != nil && !auth.ValidEditorFont(*req.EditorFont) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown editor_font"})
			return
		}
		if req.FontSize != nil && !auth.ValidFontSize(*req.FontSize) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "font_size must be between 12 and 18"})
			return
		}
		if req.Theme != nil && !auth.ValidTheme(*req.Theme) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme"})
			return
		}
		if req.EnabledLanguages != nil && !auth.ValidEnabledLanguages(*req.EnabledLanguages) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid enabled_languages"})
			return
		}
		if req.DisplayName != nil {
			name := strings.TrimSpace(*req.DisplayName)
			if len(name) > 80 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "display_name is too long"})
				return
			}
			if err := auth.SetDisplayName(r.Context(), db.Pool, user.ID, name); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		if req.TerminalFont != nil {
			if err := auth.SetTerminalFont(r.Context(), db.Pool, user.ID, *req.TerminalFont); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		if req.EditorFont != nil {
			if err := auth.SetEditorFont(r.Context(), db.Pool, user.ID, *req.EditorFont); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		if req.FontSize != nil {
			if err := auth.SetFontSize(r.Context(), db.Pool, user.ID, *req.FontSize); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		if req.Theme != nil {
			if err := auth.SetTheme(r.Context(), db.Pool, user.ID, *req.Theme); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		if req.EnabledLanguages != nil {
			if err := auth.SetEnabledLanguages(r.Context(), db.Pool, user.ID, *req.EnabledLanguages); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save preference"})
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// requestIsSecure reports whether the request arrived over TLS, either
// directly or terminated at a trusted proxy. Browsers drop Secure cookies on
// plain http:// (except localhost), so the flag must follow the actual scheme.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

type turnstileConfigResponse struct {
	Enabled bool   `json:"enabled"`
	SiteKey string `json:"site_key"`
}

// turnstileConfigHandler exposes the public Turnstile site key so the login
// page knows whether to render the widget. Unauthenticated: the site key is
// meant to be embedded in every page load and carries no secret.
func turnstileConfigHandler(siteKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, turnstileConfigResponse{Enabled: siteKey != "", SiteKey: siteKey})
	}
}

// loginHandler builds the login endpoint. turnstileSecretKey is the
// Cloudflare Turnstile secret (docs/DECISIONS.md ADR-044); empty disables
// the check entirely, so a request with no turnstile_token behaves exactly
// as before the feature existed.
func loginHandler(db *database.DB, turnstileSecretKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if turnstileSecretKey != "" {
			ok, err := turnstile.Verify(r.Context(), turnstileSecretKey, req.TurnstileToken, clientIP(r))
			if err != nil || !ok {
				audit.Record(r.Context(), db.Pool, audit.EventLoginTurnstileFailed, audit.Actor{Email: req.Email}, "user", "", nil, clientIP(r))
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "security check failed"})
				return
			}
		}

		user, err := auth.Authenticate(r.Context(), db.Pool, req.Email, req.Password)
		if errors.Is(err, auth.ErrNotFound) {
			audit.Record(r.Context(), db.Pool, audit.EventLoginFailed, audit.Actor{Email: req.Email}, "user", "", nil, clientIP(r))
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}

		// A trusted-device cookie skips the second factor entirely — the
		// password check above already happened, so this is still
		// password-plus-something, never password-only.
		if cookie, err := r.Cookie(auth.TrustedDeviceCookieName); err == nil {
			if trusted, err := auth.IsTrustedDevice(r.Context(), db.Pool, user.ID, cookie.Value); err == nil && trusted {
				completeLogin(w, r, db, user, audit.EventLoginSucceeded, false)
				return
			}
		}

		enrolled, err := auth.IsTwoFactorEnrolled(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}

		// Purpose matters (ADR-050): an unenrolled account's challenge may
		// complete enrollment (and thereby the login); an enrolled account's
		// may only verify an existing factor. resolveTwoFactorSubject
		// enforces this on the enrollment handlers.
		purpose := auth.PurposeVerify
		if !enrolled {
			purpose = auth.PurposeEnroll
		}
		token, err := auth.NewChallenge(user.ID, false, purpose)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}

		if !enrolled {
			writeJSON(w, http.StatusOK, loginChallengeResponse{RequiresEnrollment: true, ChallengeToken: token})
			return
		}

		methods, err := auth.AvailableTwoFactorMethods(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}
		writeJSON(w, http.StatusOK, loginChallengeResponse{RequiresTwoFactor: true, ChallengeToken: token, Methods: methods})
	}
}

func logoutHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(auth.CookieName); err == nil {
			if _, user, err := auth.SessionAndUser(r.Context(), db.Pool, cookie.Value); err == nil {
				audit.Record(r.Context(), db.Pool, audit.EventLogout, audit.Actor{UserID: user.ID, Email: user.Email}, "user", user.ID, nil, clientIP(r))
			}
			_ = auth.DeleteSessionByToken(r.Context(), db.Pool, cookie.Value)
		}
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}

func currentSessionHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

type sessionResponse struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

func listSessionsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())

		var currentHash string
		if cookie, err := r.Cookie(auth.CookieName); err == nil {
			currentHash = auth.HashToken(cookie.Value)
		}

		sessions, err := auth.ListSessions(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list sessions"})
			return
		}

		resp := make([]sessionResponse, 0, len(sessions))
		for _, s := range sessions {
			resp = append(resp, sessionResponse{
				ID:         s.TokenHash,
				UserAgent:  s.UserAgent,
				IPAddress:  s.IPAddress,
				CreatedAt:  s.CreatedAt,
				LastSeenAt: s.LastSeenAt,
				ExpiresAt:  s.ExpiresAt,
				Current:    s.TokenHash == currentHash,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func revokeSessionHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		sessionID := r.PathValue("sessionID")

		ok, err := auth.DeleteSessionForUser(r.Context(), db.Pool, user.ID, sessionID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke session"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// trustedProxyNets holds the parsed DEVSTATIONONE_TRUSTED_PROXIES prefixes;
// nil (the default) means no proxy is trusted and clientIP always reports
// the TCP peer address directly. Atomic because SetTrustedProxies is
// called from tests while other tests' servers are already serving.
var trustedProxyNets atomic.Pointer[[]netip.Prefix]

// SetTrustedProxies configures the proxies whose X-Forwarded-For headers
// clientIP trusts (DEVSTATIONONE_TRUSTED_PROXIES): a comma-separated list of
// IP addresses or CIDR ranges. Invalid entries are ignored (fail-secure: the
// peer address is used instead); an empty spec restores the default
// direct-RemoteAddr behavior.
func SetTrustedProxies(spec string) {
	var nets []netip.Prefix
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			if addr, err := netip.ParseAddr(part); err == nil {
				addr = addr.Unmap()
				nets = append(nets, netip.PrefixFrom(addr, addr.BitLen()))
			}
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			if a := p.Addr(); a.Is4In6() {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
			nets = append(nets, p)
		}
	}
	trustedProxyNets.Store(&nets)
}

// clientIP returns the client IP for rate limiting, audit records, and
// session rows. By default that is the TCP peer address. Only when the peer
// itself is a configured trusted proxy (DEVSTATIONONE_TRUSTED_PROXIES) is
// the leftmost valid X-Forwarded-For entry used instead — a client
// connecting directly cannot spoof the header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if peer, err := netip.ParseAddr(host); err == nil && trustedProxy(peer) {
		for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
			if ip, err := netip.ParseAddr(strings.TrimSpace(part)); err == nil {
				return ip.String()
			}
		}
	}
	return host
}

// trustedProxy reports whether peer falls inside a configured trusted-proxy
// network.
func trustedProxy(peer netip.Addr) bool {
	peer = peer.Unmap()
	nets := trustedProxyNets.Load()
	if nets == nil {
		return false
	}
	for _, p := range *nets {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}
