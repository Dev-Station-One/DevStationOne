package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"devstationone/internal/audit"
	"devstationone/internal/commandruns"
	"devstationone/internal/commands"
	"devstationone/internal/database"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// commandResponse is one stored Quick Command as returned by the API.
type commandResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toCommandResponse(c commands.Command) commandResponse {
	// timestamps serialized by writeJSON's marshaller; string fields keep
	// the shape simple — see docs/API.md "Quick Commands".
	return commandResponse{
		ID:        c.ID,
		ProjectID: c.ProjectID,
		Name:      c.Name,
		Command:   c.Command,
		SortOrder: c.SortOrder,
		CreatedAt: c.CreatedAt.Format(`"2006-01-02T15:04:05Z07:00"`),
		UpdatedAt: c.UpdatedAt.Format(`"2006-01-02T15:04:05Z07:00"`),
	}
}

func listCommandsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		list, err := commands.ListForProject(r.Context(), db.Pool, project.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list commands"})
			return
		}
		resp := make([]commandResponse, 0, len(list))
		for _, c := range list {
			resp = append(resp, toCommandResponse(c))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// decodeCommandBody validates name/command, writing the 400 response and
// returning ok=false on failure.
func decodeCommandBody(w http.ResponseWriter, r *http.Request) (commands.Fields, bool) {
	var req struct {
		Name      string `json:"name"`
		Command   string `json:"command"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return commands.Fields{}, false
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return commands.Fields{}, false
	}
	if strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return commands.Fields{}, false
	}
	return commands.Fields{Name: req.Name, Command: req.Command, SortOrder: req.SortOrder}, true
}

func createCommandHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		f, ok := decodeCommandBody(w, r)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		c, err := commands.Create(r.Context(), db.Pool, project.ID, f, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create command"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventCommandCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "command", c.ID, map[string]any{"project_id": project.ID, "name": c.Name}, clientIP(r))
		writeJSON(w, http.StatusCreated, toCommandResponse(c))
	}
}

// authorizedCommand resolves both path values and verifies the command
// belongs to the caller's project, writing a 404 (never 403, matching
// authorizedProject) on failure.
func authorizedCommand(w http.ResponseWriter, r *http.Request, db *database.DB) (commands.Command, bool) {
	c, err := commands.GetForProject(r.Context(), db.Pool, r.PathValue("projectID"), r.PathValue("commandID"))
	if errors.Is(err, commands.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "command not found"})
		return commands.Command{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up command"})
		return commands.Command{}, false
	}
	return c, true
}

func patchCommandHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedCommand(w, r, db)
		if !ok {
			return
		}
		f, ok := decodeCommandBody(w, r)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		c, err := commands.Update(r.Context(), db.Pool, existing.ID, f)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update command"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventCommandUpdated, audit.Actor{UserID: user.ID, Email: user.Email}, "command", c.ID, map[string]any{"project_id": c.ProjectID}, clientIP(r))
		writeJSON(w, http.StatusOK, toCommandResponse(c))
	}
}

func deleteCommandHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedCommand(w, r, db)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		if err := commands.Delete(r.Context(), db.Pool, existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete command"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventCommandDeleted, audit.Actor{UserID: user.ID, Email: user.Email}, "command", existing.ID, map[string]any{"name": existing.Name}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// runCommandHandler opens a new PTY session executing one Quick Command
// (shell -c <command>, ADR-045), recording it in command_runs; the captured
// output tail is flushed to the history row by the session.exit handler.
func runCommandHandler(db *database.DB, registry *runtimes.Registry, hub *sessions.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		cmd, ok := authorizedCommand(w, r, db)
		if !ok {
			return
		}
		if _, online := registry.Get(project.RuntimeID); !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}
		user, _ := userFromContext(r.Context())

		// The history row exists before the session can produce output —
		// the session.exit handler finalizes it by session_id.
		run, err := commandruns.Start(r.Context(), db.Pool, project.ID, cmd.ID, cmd.Name, cmd.Command, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record run"})
			return
		}

		sess, err := startSession(r.Context(), db, registry, hub, project, user, termSessionRequest{Name: cmd.Name, Command: cmd.Command})
		if sess.ID != "" {
			// Link even on a post-create failure: the runtime's
			// session.error/session.exit then finalizes this row.
			_ = commandruns.SetSession(r.Context(), db.Pool, run.ID, sess.ID)
		}
		if err != nil {
			// Pre-session failures never reach a session-exit event; finish
			// the row directly.
			_ = commandruns.FailRun(r.Context(), db.Pool, run.ID, err.Error())
			writeHTTPErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toTermSessionResponse(sess))
	}
}

// commandFileRPC issues one file.command RPC; unlike fileCommand it returns
// the error instead of writing a response, since the import scan treats a
// missing Makefile/package.json as a normal outcome.
func commandFileRPC(ctx context.Context, conn runtimes.Conn, cmd protocol.FileCommandPayload) (protocol.FileResultPayload, error) {
	raw, err := conn.SendCommand(ctx, protocol.MessageFileCommand, cmd)
	if err != nil {
		return protocol.FileResultPayload{}, err
	}
	var result protocol.FileResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.FileResultPayload{}, errors.New("invalid response from runtime")
	}
	return result, nil
}

// importCommandsHandler scans the project's working directory (via the
// runtime's existing file.command RPC) for Makefile targets, package.json
// scripts, and executable files in scripts/, returning candidates the UI
// offers as checkbox rows. Nothing is saved until the user accepts them.
func importCommandsHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(r.Context(), fileRPCTimeout)
		defer cancel()
		root := project.WorkingDirectory
		read := func(path string) ([]byte, bool) {
			res, err := commandFileRPC(ctx, conn, protocol.FileCommandPayload{Action: "read", Root: root, Path: path})
			if err != nil {
				return nil, false // a missing Makefile/package.json is normal
			}
			return res.Data, true
		}

		var out []commands.ImportCandidate
		if data, ok := read("Makefile"); ok {
			out = append(out, commands.ParseMakefile(data)...)
		}
		if data, ok := read("package.json"); ok {
			out = append(out, commands.ParsePackageJSON(data)...)
		}
		if res, err := commandFileRPC(ctx, conn, protocol.FileCommandPayload{Action: "list", Root: root, Path: "scripts"}); err == nil {
			out = append(out, commands.ParseScriptsDir(res.Entries)...)
		}
		if out == nil {
			out = []commands.ImportCandidate{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
	}
}

// listCommandRunsHandler serves a page of the project's run history,
// newest first, optionally scoped to one command via ?command_id=. Rows
// never carry the output tail (the detail fetch does).
func listCommandRunsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		limit, offset := 25, 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if _, err := fmt.Sscanf(v, "%d", &limit); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
				return
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if _, err := fmt.Sscanf(v, "%d", &offset); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be an integer"})
				return
			}
		}
		runs, total, err := commandruns.ListForProject(r.Context(), db.Pool, project.ID, r.URL.Query().Get("command_id"), limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list runs"})
			return
		}
		resp := make([]commandruns.Run, 0, len(runs))
		resp = append(resp, runs...)
		writeJSON(w, http.StatusOK, map[string]any{"runs": resp, "total": total, "limit": limit, "offset": offset})
	}
}

func getCommandRunHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		run, err := commandruns.GetForProject(r.Context(), db.Pool, project.ID, r.PathValue("runID"))
		if errors.Is(err, commandruns.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
			return
		} else if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up run"})
			return
		}
		writeJSON(w, http.StatusOK, run)
	}
}

// unusedImportGuard keeps filepath linked to the scripts/ import name
// derivation until it moves where it belongs.
var _ = filepath.Ext
