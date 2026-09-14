// Package api assembles the Control Server HTTP router.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"devstationone/internal/database"
	"devstationone/internal/protocol"
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

	// Per-IP rate limits (docs/SECURITY.md). One limiter instance per
	// server process (not a package-level var): each Control Server, and
	// each test's own router, gets an independent budget.
	apiRateLimit := newIPLimiter(rate.Limit(20), 40)
	loginRateLimit := newIPLimiter(rate.Every(6*time.Second), 5)

	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readyHandler(db))
	mux.HandleFunc("GET /api/v1/metrics", requireAdmin(db, metricsHandler(registry, hub)))

	mux.HandleFunc("POST /api/v1/auth/login", rateLimit(loginRateLimit, loginHandler(db)))
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
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}", requireSession(db, patchDatabaseConnectionHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}", requireSession(db, deleteDatabaseConnectionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/test", requireSession(db, testDatabaseConnectionHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/schemas", requireSession(db, listSchemasHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables", requireSession(db, listTablesHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/columns", requireSession(db, tableStructureHandler(db, registry, dbEncryptionKey, "columns", func(r protocol.DBResultPayload) any {
		if r.Columns == nil {
			return []protocol.DBColumnInfo{}
		}
		return r.Columns
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/indexes", requireSession(db, tableStructureHandler(db, registry, dbEncryptionKey, "indexes", func(r protocol.DBResultPayload) any {
		if r.Indexes == nil {
			return []protocol.DBIndexInfo{}
		}
		return r.Indexes
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/constraints", requireSession(db, tableStructureHandler(db, registry, dbEncryptionKey, "constraints", func(r protocol.DBResultPayload) any {
		if r.Constraints == nil {
			return []protocol.DBConstraintInfo{}
		}
		return r.Constraints
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/triggers", requireSession(db, tableStructureHandler(db, registry, dbEncryptionKey, "triggers", func(r protocol.DBResultPayload) any {
		if r.Triggers == nil {
			return []protocol.DBTriggerInfo{}
		}
		return r.Triggers
	})))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows", requireSession(db, listRowsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows", requireSession(db, commitRowsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/query", requireSession(db, runQueryHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/principals", requireSession(db, listPrincipalsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals", requireSession(db, createPrincipalHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}", requireSession(db, deletePrincipalHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}", requireSession(db, setPrincipalPasswordHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants", requireSession(db, listGrantsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants", requireSession(db, grantPrivilegeHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants/revoke", requireSession(db, revokePrivilegeHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/audit", requireAdmin(db, listAuditEventsHandler(db)))
	mux.HandleFunc("GET /api/v1/audit/settings", requireAdmin(db, getAuditSettingsHandler(db)))
	mux.HandleFunc("PUT /api/v1/audit/settings", requireAdmin(db, updateAuditSettingsHandler(db)))

	// StripPrefix: FileServer maps the full URL path under the root dir, so
	// /assets/x must lose the /assets/ prefix before hitting http.Dir.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

	mux.Handle("GET /", http.FileServer(http.Dir(staticDir)))

	var handler http.Handler = mux
	handler = rateLimitAPI(apiRateLimit, handler)
	handler = csrfProtect(handler)
	handler = securityHeaders(handler)
	handler = withRequestLogging(logger, handler)
	return handler
}
