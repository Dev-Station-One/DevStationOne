// Package api assembles the Control Server HTTP router.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/time/rate"

	"devstationone/assets"
	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/devices"
	"devstationone/internal/editor"
	"devstationone/internal/events"
	"devstationone/internal/mail"
	"devstationone/internal/protocol"
	"devstationone/internal/push"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
	"devstationone/web"
)

// TwoFactorConfig bundles the mandatory-2FA/passkey settings NewRouter needs
// (docs/DECISIONS.md ADR-047): SMTP for email-OTP/recovery mail, and the
// WebAuthn relying-party identity. A zero value disables email delivery
// (mail.Config.Enabled() is false) but WebAuthn still works against
// localhost defaults (see auth.NewWebAuthn), matching how tests exercise
// the router without a real mail server or public origin.
type TwoFactorConfig struct {
	SMTP             mail.Config
	WebAuthnRPID     string
	WebAuthnRPOrigin string
}

// NewRouter builds the top-level HTTP handler for the Control Server. It
// also returns the Hub, Bus, editor Hub and runtime Registry it constructed
// internally so the caller can run background loops that need them (main.go's
// idle-notification loop and the backup scheduler, docs/IMPLEMENTATION_PLAN.md)
// without a second, disconnected instance.
// dbEncryptionKey decrypts stored database connection credentials
// (docs/SECURITY.md) — see internal/dbconnections.
// staticDir selects the source for the web UI and /assets/ (ADR-048): "" (the
// production default) serves the binary's embedded web.StaticFS/assets.FS;
// a non-empty path serves web/static and assets straight from disk instead,
// so DEVSTATIONONE_DEV_STATIC edits are visible on refresh with no rebuild.
func NewRouter(db *database.DB, logger *slog.Logger, staticDir string, dbEncryptionKey []byte, turnstileSiteKey, turnstileSecretKey string, twoFactorCfg TwoFactorConfig) (http.Handler, *sessions.Hub, *events.Bus, *editor.Hub, *runtimes.Registry) {
	mux := http.NewServeMux()
	registry := runtimes.NewRegistry()
	hub := sessions.NewHub()
	bus := events.NewBus()
	editorHub := editor.NewHub()
	dockerLogsHub := newDockerLogsHub()

	wa, err := auth.NewWebAuthn(twoFactorCfg.WebAuthnRPID, twoFactorCfg.WebAuthnRPOrigin)
	if err != nil {
		logger.Error("webauthn: failed to initialize relying party, passkey endpoints will fail", "error", err)
	}

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

	// Mandatory 2FA + passkeys (docs/DECISIONS.md ADR-047). The setup/confirm
	// handlers below work both from an authenticated session (adding a
	// factor from account settings) and, with no session, from the
	// mandatory-enrollment gate driven by /auth/login's challenge_token —
	// see resolveTwoFactorSubject.
	mux.HandleFunc("GET /api/v1/auth/2fa/methods", requireSession(db, twoFactorMethodsHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/totp/setup", totpSetupHandler(db, dbEncryptionKey))
	mux.HandleFunc("POST /api/v1/auth/2fa/totp/confirm", totpConfirmHandler(db, dbEncryptionKey))
	mux.HandleFunc("DELETE /api/v1/auth/2fa/totp", requireSession(db, totpDeleteHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/email/enable", emailEnableHandler(db))
	mux.HandleFunc("DELETE /api/v1/auth/2fa/email", requireSession(db, emailDisableHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/email/request-code", rateLimit(loginRateLimit, emailRequestCodeHandler(db, twoFactorCfg.SMTP)))
	mux.HandleFunc("POST /api/v1/auth/2fa/backup-codes/regenerate", requireSession(db, backupCodesRegenerateHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/verify", rateLimit(loginRateLimit, twoFactorVerifyHandler(db, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/auth/2fa/webauthn/register/begin", webauthnRegisterBeginHandler(db, wa))
	mux.HandleFunc("POST /api/v1/auth/2fa/webauthn/register/finish", webauthnRegisterFinishHandler(db, wa))
	mux.HandleFunc("DELETE /api/v1/auth/2fa/webauthn/{credentialID}", requireSession(db, webauthnCredentialDeleteHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/webauthn/login/begin", webauthnLoginBeginHandler(db, wa))
	mux.HandleFunc("POST /api/v1/auth/2fa/webauthn/login/finish", rateLimit(loginRateLimit, webauthnLoginFinishHandler(db, wa)))
	mux.HandleFunc("POST /api/v1/auth/webauthn/passwordless/begin", webauthnPasswordlessBeginHandler(wa))
	mux.HandleFunc("POST /api/v1/auth/webauthn/passwordless/finish", rateLimit(loginRateLimit, webauthnPasswordlessFinishHandler(db, wa)))
	mux.HandleFunc("GET /api/v1/auth/trusted-devices", requireSession(db, trustedDevicesListHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/trusted-devices/{deviceID}", requireSession(db, trustedDeviceDeleteHandler(db)))
	mux.HandleFunc("POST /api/v1/auth/2fa/recover/request", rateLimit(loginRateLimit, recoveryRequestHandler(db, twoFactorCfg.SMTP)))
	mux.HandleFunc("POST /api/v1/auth/2fa/recover/confirm", rateLimit(loginRateLimit, recoveryConfirmHandler(db)))
	mux.HandleFunc("POST /api/v1/users/{userID}/2fa/reset", requireAdmin(db, adminResetTwoFactorHandler(db)))

	// Paired native-client devices (ADR-053). Pairing is unauthenticated
	// but code-gated and rate-limited; device identity rides requireIdentity
	// on the routes the Android client calls.
	deviceAuth = devices.NewAuthenticator(db.Pool, logger)
	pairRateLimit := newIPLimiter(rate.Limit(10.0/900.0), 10) // 10 attempts per 15 min per IP
	mux.HandleFunc("POST /api/v1/auth/devices/pair", rateLimit(pairRateLimit, pairDeviceHandler(db)))
	mux.HandleFunc("GET /api/v1/auth/devices", requireIdentity(db, listDevicesHandler(db)))
	mux.HandleFunc("DELETE /api/v1/auth/devices/{deviceID}", requireIdentity(db, revokeDeviceHandler(db)))
	mux.HandleFunc("GET /api/v1/users/{userID}/devices", requireAdmin(db, listUserDevicesHandler(db)))

	mux.HandleFunc("POST /api/v1/runtimes", requireAdmin(db, createRuntimeHandler(db)))
	mux.HandleFunc("GET /api/v1/runtimes", requireIdentity(db, listRuntimesHandler(db, registry)))
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
	mux.HandleFunc("GET /api/v1/projects", requireIdentity(db, listProjectsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}", requireIdentity(db, getProjectHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}", requireSession(db, patchProjectHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}", requireSession(db, deleteProjectHandler(db)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/sessions", requireIdentity(db, listTermSessionsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/sessions", requireIdentity(db, createTermSessionHandler(db, registry, hub)))

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

	mux.HandleFunc("GET /api/v1/sessions/{sessionID}", requireIdentity(db, getTermSessionHandler(db)))
	mux.HandleFunc("PATCH /api/v1/sessions/{sessionID}", requireIdentity(db, patchTermSessionHandler(db)))
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionID}", requireIdentity(db, deleteTermSessionHandler(db, registry, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/attach", requireIdentity(db, attachTermSessionHandler(db)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/detach", requireIdentity(db, detachTermSessionHandler(db, hub)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/resize", requireIdentity(db, resizeTermSessionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/sessions/{sessionID}/signal", requireIdentity(db, signalTermSessionHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/sessions/{sessionID}/stream", requireIdentity(db, termSessionStreamHandler(db, registry, hub, bus, logger)))
	mux.HandleFunc("GET /api/v1/events/stream", requireSession(db, eventsStreamHandler(bus)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker", requireSession(db, getDockerStatusHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker/resources", requireSession(db, getDockerResourcesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/docker/containers/{containerID}/logs", requireSession(db, dockerLogsStreamHandler(db, registry, dockerLogsHub)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/docker/containers/{containerID}/{dockerAction}", requireSession(db, dockerContainerActionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/docker/compose/{dockerAction}", requireSession(db, dockerComposeHandler(db, registry)))

	// Backups (ADR-041/042) and schedules, per database connection.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups", requireIdentity(db, listDBBackupsHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/backups", requireIdentity(db, createDBBackupHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireIdentity(db, getDBBackupScheduleHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireIdentity(db, putDBBackupScheduleHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/schedule", requireIdentity(db, deleteDBBackupScheduleHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}", requireIdentity(db, getDBBackupHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}", requireIdentity(db, deleteDBBackupHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/download", requireIdentity(db, downloadDBBackupHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/backups/{backupID}/restore", requireIdentity(db, restoreDBBackupHandler(db, registry, dbEncryptionKey)))

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

	// Android parity round 1 (ADR-053 addendum): list/search take a device
	// identity; the editor routes (stat/content) too since the code-editor
	// stage writes them. The writer/delete/mode-owner routes stay cookie-only.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files", requireIdentity(db, listFilesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/search", requireIdentity(db, searchFilesHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/stat", requireIdentity(db, statFileHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/files/content", requireIdentity(db, readFileHandler(db, registry)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/files/content", requireIdentity(db, writeFileHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/create", requireSession(db, createFileHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/mkdir", requireSession(db, mkdirHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/rename", requireSession(db, fileTransferHandler(db, registry, "rename")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/copy", requireSession(db, fileTransferHandler(db, registry, "copy")))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/files/move", requireSession(db, fileTransferHandler(db, registry, "move")))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/files/mode", requireSession(db, chmodFileHandler(db, registry)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/files/owner", requireSession(db, chownFileHandler(db, registry)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/files", requireSession(db, deleteFileHandler(db, registry)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/status", requireIdentity(db, gitStatusHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/diff", requireIdentity(db, gitDiffHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/stage", requireIdentity(db, gitStageHandler(db, registry, "stage", audit.EventGitStaged)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/unstage", requireIdentity(db, gitStageHandler(db, registry, "unstage", audit.EventGitUnstaged)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/commit", requireIdentity(db, gitCommitHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/log", requireIdentity(db, gitLogHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/pull", requireIdentity(db, gitPullHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/push", requireIdentity(db, gitPushHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/discard", requireIdentity(db, gitDiscardHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/amend", requireIdentity(db, gitAmendHandler(db, registry)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/git/branches", requireIdentity(db, gitBranchesHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/checkout", requireIdentity(db, gitBranchNameHandler(db, registry, "checkout", audit.EventGitCheckout)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/git/branch", requireIdentity(db, gitBranchNameHandler(db, registry, "branch", audit.EventGitBranchCreated)))

	// Journal routes accept the paired-device Sig header as well (ADR-053):
	// the Android client reads/edits project notes, and requireIdentity
	// falls back to the session cookie so browser behavior is unchanged.
	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal", requireIdentity(db, listJournalNotesHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/journal", requireIdentity(db, createJournalNoteHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal/notes/{noteID}", requireIdentity(db, getJournalNoteHandler(db)))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/journal/notes/{noteID}", requireIdentity(db, updateJournalNoteHandler(db)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/journal/notes/{noteID}", requireIdentity(db, deleteJournalNoteHandler(db)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments", requireIdentity(db, uploadJournalAttachmentHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/journal/notes/{noteID}/attachments/{attachmentID}", requireIdentity(db, downloadJournalAttachmentHandler(db)))

	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases", requireIdentity(db, createDatabaseConnectionHandler(db, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases", requireIdentity(db, listDatabaseConnectionsHandler(db)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}", requireIdentity(db, getDatabaseConnectionHandler(db)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}", requireIdentity(db, patchDatabaseConnectionHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}", requireIdentity(db, deleteDatabaseConnectionHandler(db, registry)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/test", requireIdentity(db, testDatabaseConnectionHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/schemas", requireIdentity(db, listSchemasHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables", requireIdentity(db, listTablesHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/columns", requireIdentity(db, tableStructureHandler(db, registry, dbEncryptionKey, "columns", func(r protocol.DBResultPayload) any {
		if r.Columns == nil {
			return []protocol.DBColumnInfo{}
		}
		return r.Columns
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/indexes", requireIdentity(db, tableStructureHandler(db, registry, dbEncryptionKey, "indexes", func(r protocol.DBResultPayload) any {
		if r.Indexes == nil {
			return []protocol.DBIndexInfo{}
		}
		return r.Indexes
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/constraints", requireIdentity(db, tableStructureHandler(db, registry, dbEncryptionKey, "constraints", func(r protocol.DBResultPayload) any {
		if r.Constraints == nil {
			return []protocol.DBConstraintInfo{}
		}
		return r.Constraints
	})))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/triggers", requireIdentity(db, tableStructureHandler(db, registry, dbEncryptionKey, "triggers", func(r protocol.DBResultPayload) any {
		if r.Triggers == nil {
			return []protocol.DBTriggerInfo{}
		}
		return r.Triggers
	})))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows", requireIdentity(db, listRowsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}/tables/{table}/rows", requireIdentity(db, commitRowsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/query", requireIdentity(db, runQueryHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/principals", requireIdentity(db, listPrincipalsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals", requireIdentity(db, createPrincipalHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}", requireIdentity(db, deletePrincipalHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}", requireIdentity(db, setPrincipalPasswordHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants", requireIdentity(db, listGrantsHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants", requireIdentity(db, grantPrivilegeHandler(db, registry, dbEncryptionKey)))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/databases/{connectionID}/principals/{principal}/grants/revoke", requireIdentity(db, revokePrivilegeHandler(db, registry, dbEncryptionKey)))

	mux.HandleFunc("GET /api/v1/audit", requireAdmin(db, listAuditEventsHandler(db)))
	mux.HandleFunc("GET /api/v1/audit/settings", requireAdmin(db, getAuditSettingsHandler(db)))
	mux.HandleFunc("PUT /api/v1/audit/settings", requireAdmin(db, updateAuditSettingsHandler(db)))

	mux.HandleFunc("GET /api/v1/notifications", requireSession(db, listNotificationsHandler(db)))
	mux.HandleFunc("GET /api/v1/notifications/unread-count", requireSession(db, unreadNotificationCountHandler(db)))
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", requireSession(db, markNotificationReadHandler(db)))
	mux.HandleFunc("POST /api/v1/notifications/read-all", requireSession(db, markAllNotificationsReadHandler(db)))

	// Web Push (ADR-055): browser-only, so cookie sessions, not devices.
	pushSvc, err := push.New(context.Background(), db.Pool, logger, dbEncryptionKey, twoFactorCfg.WebAuthnRPOrigin)
	if err != nil {
		logger.Error("push: disabled, web push endpoints will return 503", "error", err)
		pushSvc = nil
	} else {
		pushSvc.Attach(bus)
	}
	mux.HandleFunc("GET /api/v1/push/public-key", requireSession(db, pushPublicKeyHandler(pushSvc)))
	mux.HandleFunc("POST /api/v1/push/subscriptions", requireSession(db, pushSubscribeHandler(pushSvc)))
	mux.HandleFunc("DELETE /api/v1/push/subscriptions", requireSession(db, pushUnsubscribeHandler(pushSvc)))

	var staticFS, assetsFS fs.FS
	if staticDir == "" {
		staticFS, assetsFS = web.Static(), assets.FS
	} else {
		staticFS, assetsFS = os.DirFS(staticDir), os.DirFS("assets")
	}

	// StripPrefix: FileServer maps the full URL path under the root dir, so
	// /assets/x must lose the /assets/ prefix before hitting the FS.
	mux.Handle("GET /assets/", noCache(http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS)))))

	mux.Handle("GET /", noCache(http.FileServer(http.FS(staticFS))))

	var handler http.Handler = mux
	handler = rateLimitAPI(apiRateLimit, handler)
	handler = csrfProtect(handler)
	handler = securityHeaders(handler, staticDir != "")
	handler = withRequestLogging(logger, handler)
	return handler, hub, bus, editorHub, registry
}
