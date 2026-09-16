package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"devstationone/internal/audit"
	"devstationone/internal/backups"
	"devstationone/internal/database"
	"devstationone/internal/dbconnections"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// backupRPCTimeout bounds one backup.command RPC round-trip. start_* reply
// immediately (the job runs on the runtime and is polled); restore waits for
// the operation itself, so it gets the longer timeout.
const backupRPCTimeout = 15 * time.Second

// backupRestoreTimeout bounds a restore RPC — extraction of a full project
// archive can legitimately take minutes.
const backupRestoreTimeout = 30 * time.Minute

// backupChunkSize matches the runtime's read cap (internal/agent).
const backupChunkSize = 4 << 20

// backupCommand issues a backup.command RPC and decodes its result, writing
// an appropriate error response on failure — mirrors dbCommand.
func backupCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.BackupCommandPayload) (protocol.BackupResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), backupRPCTimeout)
	defer cancel()
	raw, err := conn.SendCommand(ctx, protocol.MessageBackupCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return protocol.BackupResultPayload{}, false
	}
	var result protocol.BackupResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
		return protocol.BackupResultPayload{}, false
	}
	return result, true
}

type backupResponse struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	ConnectionID *string   `json:"connection_id"`
	ProjectID    *string   `json:"project_id"`
	RuntimeID    string    `json:"runtime_id"`
	FileName     string    `json:"file_name"`
	SizeBytes    int64     `json:"size_bytes"`
	Trigger      string    `json:"trigger"`
	State        string    `json:"state"`
	Error        string    `json:"error"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

func toBackupResponse(b backups.Backup) backupResponse {
	return backupResponse{
		ID: b.ID, Kind: b.Kind, ConnectionID: b.ConnectionID, ProjectID: b.ProjectID,
		RuntimeID: b.RuntimeID, FileName: b.FileName, SizeBytes: b.SizeBytes,
		Trigger: b.Trigger, State: b.State, Error: b.Error,
		CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	}
}

type scheduleResponse struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Cadence    string     `json:"cadence"`
	Retention  int        `json:"retention"`
	NextRunAt  time.Time  `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastResult string     `json:"last_result"`
	LastError  string     `json:"last_error"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toScheduleResponse(s backups.Schedule) scheduleResponse {
	return scheduleResponse{
		ID: s.ID, Kind: s.Kind, Cadence: s.Cadence, Retention: s.Retention,
		NextRunAt: s.NextRunAt, LastRunAt: s.LastRunAt, LastResult: s.LastResult,
		LastError: s.LastError, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt,
	}
}

// backupWatchTimeout bounds the background goroutine watching a running
// backup job. A legitimate backup finishes well inside it; only a lost
// runtime reply (the status poll never reaching done/failed, the runtime
// never disconnecting) can hit it — the watch then marks the row failed
// instead of leaking the goroutine and row forever.
const backupWatchTimeout = 24 * time.Hour

// watchBackupJob tails one backup job's state until it leaves running,
// persisting each transition. The HTTP response already went out with
// state=running; the UI re-fetches the list.
func watchBackupJob(db *database.DB, conn runtimes.Conn, rowID, jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), backupWatchTimeout)
	defer cancel()
	state, size, jobErr := backups.AwaitJob(ctx, conn, jobID)
	if state == "" && ctx.Err() != nil {
		state, jobErr = "failed", "timed out waiting for backup job"
	}
	switch state {
	case "done":
		_ = backups.SetState(context.Background(), db.Pool, rowID, "done", size, "")
	case "failed":
		_ = backups.SetState(context.Background(), db.Pool, rowID, "failed", 0, jobErr)
	default:
		_ = backups.SetState(context.Background(), db.Pool, rowID, "failed", 0, "runtime disconnected during backup")
	}
}

// --- database backups ---

// getDBBackupHandler is GET .../backups/{backupID}.
func getDBBackupHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		b, ok := authorizedBackup(w, r, db, "db", c.ID)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toBackupResponse(b))
	}
}

// downloadDBBackupHandler is GET .../backups/{backupID}/download.
func downloadDBBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return downloadBackupHandler(db, registry)
}

// createDBBackupHandler is POST .../databases/{connectionID}/backups — start
// a manual dump on the runtime, record it, and return the running row.
func createDBBackupHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}

		backupID := uuid.NewString()
		fileName := "db/" + backupID + ".dump"
		result, ok := backupCommand(w, r, conn, protocol.BackupCommandPayload{
			Action: "start_db", BackupID: backupID, Params: &params,
		})
		if !ok {
			return
		}
		cid := c.ID
		row, err := backups.Create(r.Context(), db.Pool, "db", project.RuntimeID, fileName, "manual", user.ID, &cid, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record backup"})
			return
		}
		go watchBackupJob(db, conn, row.ID, result.JobID)
		audit.Record(r.Context(), db.Pool, audit.EventBackupCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", row.ID, map[string]any{"kind": "db", "connection_id": c.ID}, clientIP(r))
		writeJSON(w, http.StatusAccepted, toBackupResponse(row))
	}
}

func listDBBackupsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		list, err := backups.ListForConnection(r.Context(), db.Pool, c.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list backups"})
			return
		}
		out := make([]backupResponse, 0, len(list))
		for _, b := range list {
			out = append(out, toBackupResponse(b))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// authorizedBackup resolves {backupID} under the authorized project and
// confirms it belongs to that project (a db backup's connection must be the
// path's connection; a project backup's project_id must match).
func authorizedBackup(w http.ResponseWriter, r *http.Request, db *database.DB, kind, connectionID string) (backups.Backup, bool) {
	b, err := backups.Get(r.Context(), db.Pool, r.PathValue("backupID"))
	if errors.Is(err, backups.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
		return backups.Backup{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up backup"})
		return backups.Backup{}, false
	}
	if b.Kind != kind {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
		return backups.Backup{}, false
	}
	if kind == "db" && (b.ConnectionID == nil || *b.ConnectionID != connectionID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
		return backups.Backup{}, false
	}
	return b, true
}

func deleteDBBackupHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, c, _, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		b, ok := authorizedBackup(w, r, db, "db", c.ID)
		if !ok {
			return
		}
		deleteBackupRowAndFile(w, r, db, registry, b)
	}
}

// deleteBackupRowAndFile removes the runtime-side file first, then the
// metadata row (a row without its file would show a dead download).
func deleteBackupRowAndFile(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, b backups.Backup) {
	conn, online := registry.Get(b.RuntimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline; the backup file cannot be removed"})
		return
	}
	if _, ok := backupCommand(w, r, conn, protocol.BackupCommandPayload{Action: "delete", FileName: b.FileName}); !ok {
		return
	}
	if _, err := db.Pool.Exec(r.Context(), `DELETE FROM backups WHERE id = $1`, b.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete backup record"})
		return
	}
	user, _ := userFromContext(r.Context())
	audit.Record(r.Context(), db.Pool, audit.EventBackupDeleted, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", b.ID, map[string]any{"file_name": b.FileName}, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// downloadBackupHandler streams a done backup's file through the Control
// Server in 4 MiB chunks — the file never touches Control Server disk
// (ADR-041).
func downloadBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, ok := authorizedBackupAny(w, r, db)
		if !ok {
			return
		}
		if b.State != "done" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "backup is not complete"})
			return
		}
		conn, online := registry.Get(b.RuntimeID)
		if !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(b.FileName)+`"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", b.SizeBytes))
		flusher, _ := w.(http.Flusher)
		for offset := int64(0); offset < b.SizeBytes; offset += int64(backupChunkSize) {
			result, ok := backupCommand(w, r, conn, protocol.BackupCommandPayload{
				Action: "read", FileName: b.FileName, Offset: offset, Length: backupChunkSize,
			})
			if !ok {
				return // response already written; truncate the stream
			}
			if _, err := w.Write(result.Data); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// authorizedBackupAny resolves a backup under the authorized project without
// the connection-id scoping (for download, where the URL carries the
// project, not the connection).
func authorizedBackupAny(w http.ResponseWriter, r *http.Request, db *database.DB) (backups.Backup, bool) {
	project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
	if !ok {
		return backups.Backup{}, false
	}
	b, err := backups.Get(r.Context(), db.Pool, r.PathValue("backupID"))
	if errors.Is(err, backups.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
		return backups.Backup{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up backup"})
		return backups.Backup{}, false
	}
	belongs := (b.ConnectionID != nil && dbConnectionBelongsToProject(r.Context(), db, *b.ConnectionID, project.ID)) ||
		(b.ProjectID != nil && *b.ProjectID == project.ID)
	if !belongs {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
		return backups.Backup{}, false
	}
	return b, true
}

// dbConnectionBelongsToProject reports whether connectionID's stored
// connection belongs to projectID.
func dbConnectionBelongsToProject(ctx context.Context, db *database.DB, connectionID, projectID string) bool {
	c, err := dbconnections.Get(ctx, db.Pool, connectionID)
	return err == nil && c.ProjectID == projectID
}

type restoreDBRequest struct {
	// TargetDatabase is the database the backup restores into (for sqlite,
	// the target file path).
	TargetDatabase string `json:"target_database"`
	// Confirm must be true — restore overwrites the target's data, and the
	// gate is server-side, not just a UI checkbox (NFR-DB-3).
	Confirm bool `json:"confirm"`
}

// restoreDBBackupHandler is POST .../backups/{backupID}/restore — restore
// the backup into any database on the same connection.
func restoreDBBackupHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		b, ok := authorizedBackup(w, r, db, "db", c.ID)
		if !ok {
			return
		}
		if b.State != "done" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "backup is not complete"})
			return
		}
		var req restoreDBRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.TargetDatabase) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_database is required"})
			return
		}
		if !req.Confirm {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirm must be true to restore"})
			return
		}
		user, _ := userFromContext(r.Context())

		ctx, cancel := context.WithTimeout(r.Context(), backupRestoreTimeout)
		defer cancel()
		raw, err := conn.SendCommand(ctx, protocol.MessageBackupCommand, protocol.BackupCommandPayload{
			Action: "restore_db", FileName: b.FileName, TargetDatabase: req.TargetDatabase, Params: &params,
		})
		if err != nil {
			if ctx.Err() != nil {
				writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
			} else {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			}
			return
		}
		var result protocol.BackupResultPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventBackupRestored, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", b.ID, map[string]any{"target": req.TargetDatabase}, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
	}
}

type scheduleRequest struct {
	Cadence   string `json:"cadence"`
	Retention int    `json:"retention"`
}

func validateScheduleReq(req scheduleRequest) string {
	switch {
	case req.Cadence != "hourly" && req.Cadence != "daily" && req.Cadence != "weekly":
		return "cadence must be hourly, daily, or weekly"
	case req.Retention < 1 || req.Retention > 100:
		return "retention must be between 1 and 100"
	}
	return ""
}

// putDBBackupScheduleHandler is PUT .../backups/schedule.
func putDBBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		s, ok := upsertSchedule(w, r, db, "db", &c.ID, nil, user.ID)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toScheduleResponse(s))
	}
}

func deleteDBBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		deleteSchedule(w, r, db, "db", c.ID)
	}
}

func getDBBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		s, err := backups.GetScheduleForConnection(r.Context(), db.Pool, c.ID)
		if errors.Is(err, backups.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no schedule"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up schedule"})
			return
		}
		writeJSON(w, http.StatusOK, toScheduleResponse(s))
	}
}

func upsertSchedule(w http.ResponseWriter, r *http.Request, db *database.DB, kind string, connectionID, projectID *string, userID string) (backups.Schedule, bool) {
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return backups.Schedule{}, false
	}
	if msg := validateScheduleReq(req); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return backups.Schedule{}, false
	}
	s, err := backups.UpsertSchedule(r.Context(), db.Pool, kind, req.Cadence, req.Retention, connectionID, projectID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save schedule"})
		return backups.Schedule{}, false
	}
	audit.Record(r.Context(), db.Pool, audit.EventScheduleChanged, audit.Actor{UserID: userID}, "backup_schedule", s.ID, map[string]any{"kind": kind, "cadence": req.Cadence, "retention": req.Retention}, clientIP(r))
	return s, true
}

func deleteSchedule(w http.ResponseWriter, r *http.Request, db *database.DB, kind, refID string) {
	found, err := backups.DeleteSchedule(r.Context(), db.Pool, kind, refID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete schedule"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no schedule"})
		return
	}
	audit.Record(r.Context(), db.Pool, audit.EventScheduleChanged, audit.Actor{}, "backup_schedule", refID, map[string]any{"kind": kind, "action": "deleted"}, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// --- project backups ---

// createProjectBackupHandler is POST /api/v1/projects/{projectID}/backups.
func createProjectBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		conn, online := registry.Get(project.RuntimeID)
		if !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}
		user, _ := userFromContext(r.Context())

		backupID := uuid.NewString()
		fileName := "projects/" + backupID + ".tar.gz"
		result, ok := backupCommand(w, r, conn, protocol.BackupCommandPayload{
			Action: "start_project", BackupID: backupID, SourceDir: project.WorkingDirectory,
		})
		if !ok {
			return
		}
		pid := project.ID
		row, err := backups.Create(r.Context(), db.Pool, "project", project.RuntimeID, fileName, "manual", user.ID, nil, &pid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record backup"})
			return
		}
		go watchBackupJob(db, conn, row.ID, result.JobID)
		audit.Record(r.Context(), db.Pool, audit.EventBackupCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", row.ID, map[string]any{"kind": "project", "project_id": project.ID}, clientIP(r))
		writeJSON(w, http.StatusAccepted, toBackupResponse(row))
	}
}

func listProjectBackupsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		list, err := backups.ListForProject(r.Context(), db.Pool, project.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list backups"})
			return
		}
		out := make([]backupResponse, 0, len(list))
		for _, b := range list {
			out = append(out, toBackupResponse(b))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// getProjectBackupHandler is GET .../backups/{backupID}.
func getProjectBackupHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		b, err := backups.Get(r.Context(), db.Pool, r.PathValue("backupID"))
		if errors.Is(err, backups.ErrNotFound) || (err == nil && (b.Kind != "project" || b.ProjectID == nil || *b.ProjectID != project.ID)) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up backup"})
			return
		}
		writeJSON(w, http.StatusOK, toBackupResponse(b))
	}
}

// downloadProjectBackupHandler is GET .../backups/{backupID}/download.
func downloadProjectBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return downloadBackupHandler(db, registry)
}

func deleteProjectBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		b, err := backups.Get(r.Context(), db.Pool, r.PathValue("backupID"))
		if errors.Is(err, backups.ErrNotFound) || (err == nil && (b.Kind != "project" || b.ProjectID == nil || *b.ProjectID != project.ID)) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up backup"})
			return
		}
		deleteBackupRowAndFile(w, r, db, registry, b)
	}
}

// closeProjectSessions closes every open terminal session of project — used
// before an overwrite-restore extracts into the project's directory
// (FR-PJ-8). The Control Server owns this; the runtime never closes sessions
// itself.
func closeProjectSessions(ctx context.Context, db *database.DB, registry *runtimes.Registry, projectID string) error {
	list, err := sessions.ListForProject(ctx, db.Pool, projectID)
	if err != nil {
		return err
	}
	terminal := map[string]bool{
		sessions.StatusExited: true, sessions.StatusFailed: true, sessions.StatusClosed: true,
	}
	for _, s := range list {
		if terminal[s.Status] {
			continue
		}
		project, err := projects.Get(ctx, db.Pool, projectID)
		if err != nil {
			return err
		}
		if conn, online := registry.Get(project.RuntimeID); online {
			_ = conn.SendEnvelope(protocol.MessageSessionClose, protocol.SessionClosePayload{SessionID: s.ID})
		}
		_ = sessions.MarkClosed(ctx, db.Pool, s.ID)
	}
	return nil
}

type restoreProjectRequest struct {
	// Mode is "new" or "overwrite".
	Mode string `json:"mode"`
	// ConfirmName must repeat the project's current name verbatim for
	// overwrite mode — server-side gate (FR-PJ-5).
	ConfirmName string `json:"confirm_name"`
}

// restoreProjectBackupHandler is POST .../backups/{backupID}/restore.
func restoreProjectBackupHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		b, err := backups.Get(r.Context(), db.Pool, r.PathValue("backupID"))
		if errors.Is(err, backups.ErrNotFound) || (err == nil && (b.Kind != "project" || b.ProjectID == nil || *b.ProjectID != project.ID)) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up backup"})
			return
		}
		if b.State != "done" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "backup is not complete"})
			return
		}
		var req restoreProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		switch req.Mode {
		case "overwrite":
			restoreProjectOverwrite(w, r, db, registry, project, b, req.ConfirmName)
		case "new":
			restoreProjectAsNew(w, r, db, registry, project, b)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `mode must be "new" or "overwrite"`})
		}
	}
}

// restoreProjectOverwrite: close sessions, take an automatic safety
// snapshot, wipe the directory and extract (the snapshot first, so a failed
// extraction still leaves the pre-restore archive).
func restoreProjectOverwrite(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, project projects.Project, b backups.Backup, confirmName string) {
	if confirmName != project.Name {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirm_name must match the project name exactly"})
		return
	}
	user, _ := userFromContext(r.Context())

	if err := closeProjectSessions(r.Context(), db, registry, project.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to close open sessions"})
		return
	}

	conn, online := registry.Get(project.RuntimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
		return
	}

	// Safety snapshot of the current directory, recorded as pre_restore.
	snapshotID := uuid.NewString()
	snapResult, ok := backupCommand(w, r, conn, protocol.BackupCommandPayload{
		Action: "start_project", BackupID: snapshotID, SourceDir: project.WorkingDirectory,
	})
	if !ok {
		return
	}
	snapRow, err := backups.Create(r.Context(), db.Pool, "project", project.RuntimeID,
		"projects/"+snapshotID+".tar.gz", "pre_restore", user.ID, nil, &project.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record snapshot"})
		return
	}
	go watchBackupJob(db, conn, snapRow.ID, snapResult.JobID)

	// Wait for the snapshot to finish before wiping the directory.
	ctx, cancel := context.WithTimeout(r.Context(), backupRestoreTimeout)
	defer cancel()
	state, _, _ := backups.AwaitJob(ctx, conn, snapResult.JobID)
	if state != "done" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "pre-restore snapshot failed; nothing was overwritten"})
		return
	}

	restoreCtx, cancelRestore := context.WithTimeout(r.Context(), backupRestoreTimeout)
	defer cancelRestore()
	_, err = conn.SendCommand(restoreCtx, protocol.MessageBackupCommand, protocol.BackupCommandPayload{
		Action: "restore_project", FileName: b.FileName, RestoreDir: project.WorkingDirectory,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	audit.Record(r.Context(), db.Pool, audit.EventBackupRestored, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", b.ID, map[string]any{"mode": "overwrite", "project_id": project.ID}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored", "snapshot_id": snapRow.ID})
}

// projectNameSanitizer strips everything a directory name can't safely
// contain; derived restore directories use it (requirements: derived from
// runtime root + sanitized project name).
var projectNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// restoreProjectAsNew creates a new Project record and extracts the archive
// into a fresh working directory derived from the runtime root + sanitized
// name, collision-suffixed (-2, -3, ...).
func restoreProjectAsNew(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, project projects.Project, b backups.Backup) {
	rt, err := runtimes.GetByID(r.Context(), db.Pool, b.RuntimeID)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "backup's runtime not found"})
		return
	}
	homeDir := rt.Capabilities.HomeDir
	if homeDir == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime has not reported a home directory yet"})
		return
	}
	conn, online := registry.Get(b.RuntimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
		return
	}

	base := strings.ToLower(projectNameUnsafe.ReplaceAllString(strings.TrimSpace(project.Name), "-"))
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "restored-project"
	}
	dir := ""
	for i := 1; i <= 100; i++ {
		candidate := filepath.Join(homeDir, base)
		if i > 1 {
			candidate = filepath.Join(homeDir, fmt.Sprintf("%s-%d", base, i))
		}
		if !dirExists(r, conn, rt.Capabilities.HomeDir, candidate) {
			dir = candidate
			break
		}
	}
	if dir == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not find a free directory name"})
		return
	}

	newName := fmt.Sprintf("%s restored %s", project.Name, time.Now().UTC().Format("2006-01-02"))
	p, err := projects.Create(r.Context(), db.Pool, projects.Fields{
		Name:             newName,
		Description:      "Restored from backup " + b.ID,
		RuntimeID:        b.RuntimeID,
		WorkingDirectory: dir,
		Shell:            project.Shell,
		Environment:      project.Environment,
	}, b.CreatedBy)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create restored project"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), backupRestoreTimeout)
	defer cancel()
	_, err = conn.SendCommand(ctx, protocol.MessageBackupCommand, protocol.BackupCommandPayload{
		Action: "restore_project", FileName: b.FileName, RestoreDir: dir,
	})
	if err != nil {
		// Roll the record back — a project whose directory was never
		// populated would only confuse.
		_, _ = projects.Delete(r.Context(), db.Pool, p.ID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	user, _ := userFromContext(r.Context())
	audit.Record(r.Context(), db.Pool, audit.EventBackupRestored, audit.Actor{UserID: user.ID, Email: user.Email}, "backup", b.ID, map[string]any{"mode": "new", "project_id": p.ID, "working_directory": dir}, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"status": "restored", "project_id": p.ID, "name": p.Name, "working_directory": p.WorkingDirectory})
}

// dirExists checks one absolute path under the runtime's home directory via
// file.command stat (same RPC the directory picker uses).
func dirExists(r *http.Request, conn runtimes.Conn, root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true // can't verify; treat as taken to avoid overwriting
	}
	raw, err := conn.SendCommand(r.Context(), protocol.MessageFileCommand, protocol.FileCommandPayload{Action: "stat", Root: root, Path: rel})
	if err != nil {
		return false
	}
	var result protocol.FileResultPayload
	if json.Unmarshal(raw, &result) != nil {
		return false
	}
	return result.Info != nil
}

// --- project schedule ---

func putProjectBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		s, ok := upsertSchedule(w, r, db, "project", nil, &project.ID, user.ID)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toScheduleResponse(s))
	}
}

func getProjectBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		s, err := backups.GetScheduleForProject(r.Context(), db.Pool, project.ID)
		if errors.Is(err, backups.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no schedule"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up schedule"})
			return
		}
		writeJSON(w, http.StatusOK, toScheduleResponse(s))
	}
}

func deleteProjectBackupScheduleHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		deleteSchedule(w, r, db, "project", project.ID)
	}
}
