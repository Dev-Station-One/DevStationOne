// Package api assembles the Control Server HTTP router.
package api

import (
	"log/slog"
	"net/http"

	"devstation/internal/database"
	"devstation/internal/runtimes"
)

// NewRouter builds the top-level HTTP handler for the Control Server.
func NewRouter(db *database.DB, logger *slog.Logger, staticDir string) http.Handler {
	mux := http.NewServeMux()
	registry := runtimes.NewRegistry()

	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readyHandler(db))

	mux.HandleFunc("POST /api/v1/auth/login", loginHandler(db))
	mux.HandleFunc("POST /api/v1/auth/logout", logoutHandler(db))
	mux.HandleFunc("GET /api/v1/auth/session", requireSession(db, currentSessionHandler))
	mux.HandleFunc("GET /api/v1/auth/sessions", requireSession(db, listSessionsHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{sessionID}", requireSession(db, revokeSessionHandler(db)))

	mux.HandleFunc("POST /api/v1/runtimes", requireAdmin(db, createRuntimeHandler(db)))
	mux.HandleFunc("GET /api/v1/runtimes", requireSession(db, listRuntimesHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/revoke", requireAdmin(db, revokeRuntimeHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/connect", runtimeConnectHandler(db, registry, logger))

	mux.HandleFunc("POST /api/v1/projects", requireSession(db, createProjectHandler(db)))
	mux.HandleFunc("GET /api/v1/projects", requireSession(db, listProjectsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}", requireSession(db, getProjectHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}", requireSession(db, patchProjectHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}", requireSession(db, deleteProjectHandler(db)))

	mux.Handle("GET /", http.FileServer(http.Dir(staticDir)))

	return withRequestLogging(logger, mux)
}

func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
	})
}
