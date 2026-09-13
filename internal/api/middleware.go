package api

import (
	"context"
	"net/http"

	"devstation/internal/auth"
	"devstation/internal/database"
)

type contextKey int

const userContextKey contextKey = 0

// userFromContext returns the authenticated user attached by requireSession.
func userFromContext(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userContextKey).(auth.User)
	return u, ok
}

// requireSession authenticates the request via the session cookie and rejects
// it with 401 if there is no valid session.
func requireSession(db *database.DB, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		_, user, err := auth.SessionAndUser(r.Context(), db.Pool, cookie.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin builds on requireSession, additionally rejecting the request
// with 403 if the authenticated user is not an administrator.
func requireAdmin(db *database.DB, next http.HandlerFunc) http.HandlerFunc {
	return requireSession(db, func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		if !user.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator access required"})
			return
		}
		next(w, r)
	})
}
