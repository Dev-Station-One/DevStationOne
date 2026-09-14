package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/filesystem"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// gitRPCTimeout bounds how long a git REST request waits for the runtime to
// perform the operation and reply.
const gitRPCTimeout = 30 * time.Second

// gitCommand issues a git.command RPC to the project's runtime and decodes
// its result, writing an appropriate error response on failure.
func gitCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.GitCommandPayload) (protocol.GitResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), gitRPCTimeout)
	defer cancel()

	raw, err := conn.SendCommand(ctx, protocol.MessageGitCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return protocol.GitResultPayload{}, false
	}

	var result protocol.GitResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
		return protocol.GitResultPayload{}, false
	}
	return result, true
}

// gitStatusHandler GET .../git/status — the changed-file list.
func gitStatusHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		result, ok := gitCommand(w, r, conn, protocol.GitCommandPayload{Action: "status", Root: root})
		if !ok {
			return
		}
		if result.Files == nil {
			result.Files = []protocol.GitFileStatus{}
		}
		writeJSON(w, http.StatusOK, result.Files)
	}
}

// gitDiffHandler GET .../git/diff?path=&staged= — a unified diff.
func gitDiffHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p := r.URL.Query().Get("path")
		if p != "" {
			var err error
			if p, err = filesystem.Clean(p); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		result, ok := gitCommand(w, r, conn, protocol.GitCommandPayload{
			Action: "diff",
			Root:   root,
			Path:   p,
			Staged: r.URL.Query().Get("staged") == "1",
		})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"diff": result.Diff})
	}
}

// gitStageHandler builds the POST .../git/stage and .../git/unstage
// handlers — both take {"path"} and reply 204.
func gitStageHandler(db *database.DB, registry *runtimes.Registry, action, eventType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		p, err := filesystem.Clean(body.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		body.Path = p
		if _, ok := gitCommand(w, r, conn, protocol.GitCommandPayload{Action: action, Root: root, Path: body.Path}); !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, eventType, audit.Actor{UserID: user.ID, Email: user.Email}, "git", body.Path, map[string]any{"project_id": r.PathValue("projectID")}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// gitCommitHandler POST .../git/commit {"message"} — commits the staged
// index, replying with the new commit's SHA.
func gitCommitHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
			return
		}
		result, ok := gitCommand(w, r, conn, protocol.GitCommandPayload{Action: "commit", Root: root, Message: body.Message})
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventGitCommitted, audit.Actor{UserID: user.ID, Email: user.Email}, "git", result.Commit, map[string]any{"project_id": r.PathValue("projectID")}, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]string{"commit": result.Commit})
	}
}
