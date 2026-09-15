// Package api assembles the Control Server HTTP router.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/editor"
	"devstationone/internal/events"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// NewRouter builds the top-level HTTP handler for the Control Server. It
// also returns the Hub, Bus, editor Hub and runtime Registry it constructed
// internally so the caller can run background loops that need them (main.go's
// idle-notification loop and the backup scheduler, docs/IMPLEMENTATION_PLAN.md)
// without a second, disconnected instance.
// dbEncryptionKey decrypts stored database connection credentials
// (docs/SECURITY.md) — see internal/dbconnections.
func NewRouter(db *database.DB, logger *slog.Logger, staticDir string, dbEncryptionKey []byte, turnstileSiteKey, turnstileSecretKey string) (http.Handler, *sessions.Hub, *events.Bus, *editor.Hub, *runtimes.Registry) {
	mux := http.NewServeMux()
	registry := runtimes.NewRegistry()
	hub := sessions.NewHub()
	bus := events.NewBus()
	editorHub := editor.NewHub()
	dockerLogsHub := newDockerLogsHub()

	// Per-IP rate limits (docs/SECURITY.md). One limiter instance per
	// server process (not a package-level var): each Control Server, and
	// each test's own router, gets an independent budget.
	apiRateLimit := newIPLimiter(rate.Limit(20), 40)
	loginRateLimit := newIPLimiter(rate.Every(6*time.Second), 5)

	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readyHandler(db))
	mux.HandleFunc("GET /api/v1/metrics", requireAdmin(db, metricsHandler(registry, hub)))

	// User manager (admin only, ADR-038).
	mux.HandleFunc("GET /api/v1/users", requireAdmin(db, listUsersHandler(db)))
	mux.HandleFunc("POST /api/v1/users", requireAdmin(db, createUserHandler(db)))
	mux.HandleFunc("PATCH /api/v1/users/{userID}", requireAdmin(db, patchUserHandler(db)))
	mux.HandleFunc("DELETE /api/v1/users/{userID}", requireAdmin(db, deleteUserHandler(db)))
	mux.HandleFunc("GET /api/v1/users/{userID}/resources", requireAdmin(db, getUserResourcesHandler(db)))
	mux.HandleFunc("POST /api/v1/users/{userID}/reassign", requireAdmin(db, reassignUserResourcesHandler(db)))

	mux.HandleFunc("GET /api/v1/auth/turnstile-config", turnstileConfigHandler(turnstileSiteKey))
	mux.HandleFunc("POST /api/v1/auth/login", rateLimit(loginRateLimit, loginHandler(db, turnstileSecretKey)))
	mux.HandleFunc("POST /api/v1/auth/logout", logoutHandler(db))
	mux.HandleFunc("GET /api/v1/auth/session", requireSession(db, currentSessionHandler))
	mux.HandleFunc("PUT /api/v1/auth/preferences", requireSession(db, updatePreferencesHandler(db)))
	mux.HandleFunc("GET /api/v1/auth/sessions", requireSession(db, listSessionsHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{sessionID}", requireSession(db, revokeSessionHandler(db)))

	mux.HandleFunc("POST /api/v1/runtimes", requireAdmin(db, createRuntimeHandler(db)))
	mux.HandleFunc("GET /api/v1/runtimes", requireSession(db, listRuntimesHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/revoke", requireAdmin(db, revokeRuntimeHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/runtimes/{runtimeID}", requireAdmin(db, deleteRuntimeHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/reactivate", requireAdmin(db, reactivateRuntimeHandler(db)))
	mux.HandleFunc("GET /api/v1/runtimes/connect", runtimeConnectHandler(db, registry, hub, bus, editorHub, dockerLogsHub, logger))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/browse", requireSession(db, browseRuntimeHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/browse/search", requireSession(db, browseSearchHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/browse/mkdir", requireSession(db, browseMkdirHandler(db, registry)))

	// Runtime-level service management (ADR-043) — services belong to the
	// runtime host, not to a project.
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/services", requireSession(db, listServicesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/services/{unit}", requireSession(db, getServiceHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/runtimes/{runtimeID}/services/{unit}/logs", requireSession(db, getServiceLogsHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/services/{unit}/start", requireSession(db, serviceActionHandler(db, registry, "start")))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/services/{unit}/stop", requireSession(db, serviceActionHandler(db, registry, "stop")))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/services/{unit}/restart", requireSession(db, serviceActionHandler(db, registry, "restart")))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/services/{unit}/enable", requireSession(db, serviceActionHandler(db, registry, "enable")))
	mux.HandleFunc("POST /api/v1/runtimes/{runtimeID}/services/{unit}/disable", requireSession(db, serviceActionHandler(db, registry, "disable")))

	mux.HandleFunc("POST /api/v1/projects", requireSession(db, createProjectHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects", requireSession(db, listProjectsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}", requireSession(db, getProjectHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}", requireSession(db, patchProjectHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}", requireSession(db, deleteProjectHandler(db)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/sessions", requireSession(db, listTermSessionsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/sessions", requireSession(db, createTermSessionHandler(db, registry, hub)))

	// Quick Commands (ADR-045): per-project click-to-run CLI commands, each
	// opening a real PTY session, with run history under command-runs.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/commands", requireSession(db, listCommandsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/commands", requireSession(db, createCommandHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/commands/import", requireSession(db, importCommandsHandler(db, registry)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/commands/{commandID}", requireSession(db, patchCommandHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/commands/{commandID}", requireSession(db, deleteCommandHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/commands/{commandID}/run", requireSession(db, runCommandHandler(db, registry, hub)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/command-runs", requireSession(db, listCommandRunsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/command-runs/{runID}", requireSession(db, getCommandRunHandler(db)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/editor/stream", requireSession(db, editorStreamHandler(db, registry, editorHub, logger)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/editor/state", requireSession(db, getEditorStateHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/editor/state", requireSession(db, putEditorStateHandler(db)))

	mux.HandleFunc("GET /api/v1/sessions/{sessionID}", requireSession(db, getTermSessionHandler(db)))
	mux.HandleFunc("PATCH /api/v1/sessions/{sessionID}", requireSession(db, patchTermSessionHandler(db)))
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionID}", requireSession(db, deleteTermSessionHandler(db, registry, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/attach", requireSession(db, attachTermSessionHandler(db)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/detach", requireSession(db, detachTermSessionHandler(db, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/resize", requireSession(db, resizeTermSessionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/signal", requireSession(db, signalTermSessionHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/stream", requireSession(db, termSessionStreamHandler(db, registry, hub, bus, logger)))
	mux.HandleFunc("GET /api/v1/events/stream", requireSession(db, eventsStreamHandler(bus)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker", requireSession(db, getDockerStatusHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker/resources", requireSession(db, getDockerResourcesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker/containers/{containerID}/logs", requireSession(db, dockerLogsStreamHandler(db, registry, dockerLogsHub)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/docker/containers/{containerID}/{dockerAction}", requireSession(db, dockerContainerActionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/docker/compose/{dockerAction}", requireSession(db, dockerComposeHandler(db, registry)))

	// Backups (ADR-041/042) and schedules, per database connection.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups", requireSession(db, listDBBackupsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/backups", requireSession(db, createDBBackupHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireSession(db, getDBBackupScheduleHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireSession(db, putDBBackupScheduleHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireSession(db, deleteDBBackupScheduleHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}", requireSession(db, getDBBackupHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}", requireSession(db, deleteDBBackupHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/download", requireSession(db, downloadDBBackupHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/restore", requireSession(db, restoreDBBackupHandler(db, registry, dbEncryptionKey)))

	// Project backups.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/backups", requireSession(db, listProjectBackupsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/backups", requireSession(db, createProjectBackupHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/backups/schedule", requireSession(db, getProjectBackupScheduleHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/backups/schedule", requireSession(db, putProjectBackupScheduleHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/backups/schedule", requireSession(db, deleteProjectBackupScheduleHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/backups/{backupID}", requireSession(db, getProjectBackupHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/backups/{backupID}", requireSession(db, deleteProjectBackupHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/backups/{backupID}/download", requireSession(db, downloadProjectBackupHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/backups/{backupID}/restore", requireSession(db, restoreProjectBackupHandler(db, registry)))

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
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/files/mode", requireSession(db, chmodFileHandler(db, registry)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/files/owner", requireSession(db, chownFileHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/files", requireSession(db, deleteFileHandler(db, registry)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/status", requireSession(db, gitStatusHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/diff", requireSession(db, gitDiffHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/stage", requireSession(db, gitStageHandler(db, registry, "stage", audit.EventGitStaged)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/unstage", requireSession(db, gitStageHandler(db, registry, "unstage", audit.EventGitUnstaged)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/commit", requireSession(db, gitCommitHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/log", requireSession(db, gitLogHandler(db, registry)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal", requireSession(db, listJournalNotesHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/journal", requireSession(db, createJournalNoteHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal/notes/{noteID}", requireSession(db, getJournalNoteHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/journal/notes/{noteID}", requireSession(db, updateJournalNoteHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/journal/notes/{noteID}", requireSession(db, deleteJournalNoteHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments", requireSession(db, uploadJournalAttachmentHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments/{attachmentID}", requireSession(db, downloadJournalAttachmentHandler(db)))

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

	mux.HandleFunc("GET /api/v1/notifications", requireSession(db, listNotificationsHandler(db)))
	mux.HandleFunc("GET /api/v1/notifications/unread-count", requireSession(db, unreadNotificationCountHandler(db)))
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", requireSession(db, markNotificationReadHandler(db)))
	mux.HandleFunc("POST /api/v1/notifications/read-all", requireSession(db, markAllNotificationsReadHandler(db)))

	// StripPrefix: FileServer maps the full URL path under the root dir, so
	// /assets/x must lose the /assets/ prefix before hitting http.Dir.
	mux.Handle("GET /assets/", noCache(http.StripPrefix("/assets/", http.FileServer(http.Dir("assets")))))

	mux.Handle("GET /", noCache(http.FileServer(http.Dir(staticDir))))

	var handler http.Handler = mux
	handler = rateLimitAPI(apiRateLimit, handler)
	handler = csrfProtect(handler)
	handler = securityHeaders(handler)
	handler = withRequestLogging(logger, handler)
	return handler, hub, bus, editorHub, registry
}
