// Package api assembles the Control Server HTTP router.
package api

import (
	"log/slog"
	"net/http"

	"devstationone/internal/database"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// NewRouter builds the top-level HTTP handler for the Control Server.
// dbEncryptionKey decrypts stored database connection credentials
// (docs/SECURITY.md) — see internal/dbconnections.
func NewRouter(db *database.DB, logger *slog.Logger, staticDir string, dbEncryptionKey []byte) http.Handler {
	mux := http.NewServeMux()
	registry := runtimes.NewRegistry()
	hub := sessions.NewHub()

	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readyHandler(db))

	mux.HandleFunc("POST /api/v1/auth/login", loginHandler(db))
	mux.HandleFunc("POST /api/v1/auth/logout", logoutHandler(db))
	mux.HandleFunc("GET /api/v1/auth/session", requireSession(db, currentSessionHandler))
	mux.HandleFunc("PUT /api/v1/auth/preferences", requireSession(db, updatePreferencesHandler(db)))
	mux.HandleFunc("GET /api/v1/auth/sessions", requireSession(db, listSessionsHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{sessionID}", requireSession(db, revokeSessionHandler(db)))

	mux.HandleFunc("POST /api/v1/runtimes", requireAdmin(db, createRuntimeHandler(db)))
	mux.HandleFunc("GET /api/v1/runtimes", requireSession(db, listRuntimesHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/revoke", requireAdmin(db, revokeRuntimeHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/connect", runtimeConnectHandler(db, registry, hub, logger))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/browse", requireSession(db, browseRuntimeHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/browse/search", requireSession(db, browseSearchHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/browse/mkdir", requireSession(db, browseMkdirHandler(db, registry)))

	mux.HandleFunc("POST /api/v1/projects", requireSession(db, createProjectHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects", requireSession(db, listProjectsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}", requireSession(db, getProjectHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}", requireSession(db, patchProjectHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}", requireSession(db, deleteProjectHandler(db)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/sessions", requireSession(db, listTermSessionsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/sessions", requireSession(db, createTermSessionHandler(db, registry, hub)))

	mux.HandleFunc("GET /api/v1/sessions/{sessionID}", requireSession(db, getTermSessionHandler(db)))
	mux.HandleFunc("PATCH /api/v1/sessions/{sessionID}", requireSession(db, patchTermSessionHandler(db)))
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionID}", requireSession(db, deleteTermSessionHandler(db, registry, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/attach", requireSession(db, attachTermSessionHandler(db)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/detach", requireSession(db, detachTermSessionHandler(db, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/resize", requireSession(db, resizeTermSessionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/signal", requireSession(db, signalTermSessionHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/stream", requireSession(db, termSessionStreamHandler(db, registry, hub, logger)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/services", requireSession(db, listServicesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{unit}", requireSession(db, getServiceHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{unit}/logs", requireSession(db, getServiceLogsHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{unit}/start", requireSession(db, serviceActionHandler(db, registry, "start")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{unit}/stop", requireSession(db, serviceActionHandler(db, registry, "stop")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{unit}/restart", requireSession(db, serviceActionHandler(db, registry, "restart")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{unit}/enable", requireSession(db, serviceActionHandler(db, registry, "enable")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{unit}/disable", requireSession(db, serviceActionHandler(db, registry, "disable")))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/files", requireSession(db, listFilesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/search", requireSession(db, searchFilesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/stat", requireSession(db, statFileHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/content", requireSession(db, readFileHandler(db, registry)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/files/content", requireSession(db, writeFileHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/create", requireSession(db, createFileHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/mkdir", requireSession(db, mkdirHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/rename", requireSession(db, fileTransferHandler(db, registry, "rename")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/copy", requireSession(db, fileTransferHandler(db, registry, "copy")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/move", requireSession(db, fileTransferHandler(db, registry, "move")))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/files", requireSession(db, deleteFileHandler(db, registry)))

	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases", requireSession(db, createDatabaseConnectionHandler(db, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases", requireSession(db, listDatabaseConnectionsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}", requireSession(db, getDatabaseConnectionHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}", requireSession(db, patchDatabaseConnectionHandler(db, dbEncryptionKey)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}", requireSession(db, deleteDatabaseConnectionHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/test", requireSession(db, testDatabaseConnectionHandler(db, registry, dbEncryptionKey)))

	// StripPrefix: FileServer maps the full URL path under the root dir, so
	// /assets/x must lose the /assets/ prefix before hitting http.Dir.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

	mux.Handle("GET /", http.FileServer(http.Dir(staticDir)))

	return withRequestLogging(logger, mux)
}

func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
	})
}
