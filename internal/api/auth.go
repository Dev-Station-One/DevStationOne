package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"devstationone/internal/auth"
	"devstationone/internal/database"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	IsAdmin      bool      `json:"is_admin"`
	TerminalFont string    `json:"terminal_font"`
	Theme        string    `json:"theme"`
	CreatedAt    time.Time `json:"created_at"`
}

func toUserResponse(u auth.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin, TerminalFont: u.TerminalFont, Theme: u.Theme, CreatedAt: u.CreatedAt}
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
			TerminalFont *string `json:"terminal_font"`
			Theme        *string `json:"theme"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.TerminalFont == nil && req.Theme == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to update"})
			return
		}
		if req.TerminalFont != nil && !auth.ValidTerminalFont(*req.TerminalFont) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown terminal_font"})
			return
		}
		if req.Theme != nil && !auth.ValidTheme(*req.Theme) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme"})
			return
		}
		if req.TerminalFont != nil {
			if err := auth.SetTerminalFont(r.Context(), db.Pool, user.ID, *req.TerminalFont); err != nil {
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

func loginHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		user, err := auth.Authenticate(r.Context(), db.Pool, req.Email, req.Password)
		if errors.Is(err, auth.ErrNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}

		token, err := auth.CreateSession(r.Context(), db.Pool, user.ID, r.UserAgent(), clientIP(r))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}

		setSessionCookie(w, r, token)
		writeJSON(w, http.StatusOK, toUserResponse(user))
	}
}

func logoutHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(auth.CookieName); err == nil {
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
