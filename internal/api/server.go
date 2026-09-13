// Package api assembles the Control Server HTTP router.
package api

import (
	"log/slog"
	"net/http"

	"devstation/internal/database"
)

// NewRouter builds the top-level HTTP handler for the Control Server.
func NewRouter(db *database.DB, logger *slog.Logger, staticDir string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readyHandler(db))

	mux.HandleFunc("POST /api/v1/auth/login", loginHandler(db))
	mux.HandleFunc("POST /api/v1/auth/logout", logoutHandler(db))
	mux.HandleFunc("GET /api/v1/auth/session", requireSession(db, currentSessionHandler))
	mux.HandleFunc("GET /api/v1/auth/sessions", requireSession(db, listSessionsHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{sessionID}", requireSession(db, revokeSessionHandler(db)))

	mux.Handle("GET /", http.FileServer(http.Dir(staticDir)))

	return withRequestLogging(logger, mux)
}

func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
	})
}
