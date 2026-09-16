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
// perform a local operation and reply.
const gitRPCTimeout = 30 * time.Second

// gitNetworkRPCTimeout bounds pull/push, matching
// internal/agent/git_manager.go's gitNetworkCommandTimeout so the Control
// Server doesn't time out the HTTP request before the runtime's own longer
// deadline would.
const gitNetworkRPCTimeout = 2 * time.Minute

// gitCommand issues a git.command RPC to the project's runtime and decodes
// its result, writing an appropriate error response on failure.
func gitCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.GitCommandPayload) (protocol.GitResultPayload, bool) {
	return gitCommandTimeout(w, r, conn, cmd, gitRPCTimeout)
}

// gitCommandTimeout is gitCommand with an explicit RPC timeout, for actions
// (pull/push) that need longer than the default.
func gitCommandTimeout(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.GitCommandPayload, timeout time.Duration) (protocol.GitResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
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

// gitStatusHandler GET .../git/status — the changed-file list plus the
// current branch's upstream tracking state.
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
		if result.Branch == nil {
			result.Branch = &protocol.GitBranchStatus{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": result.Files, "branch": result.Branch})
	}
}

// gitPullHandler POST .../git/pull — pulls the current branch's upstream. A
// merge conflict is not an error: the response is 200 with a non-empty
// "conflicts" array, matching what running `git pull` by hand would leave
// behind for the user to resolve via the Terminal tab.
func gitPullHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		result, ok := gitCommandTimeout(w, r, conn, protocol.GitCommandPayload{Action: "pull", Root: root}, gitNetworkRPCTimeout)
		if !ok {
			return
		}
		if result.Conflicts == nil {
			result.Conflicts = []protocol.GitFileStatus{}
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventGitPulled, audit.Actor{UserID: user.ID, Email: user.Email}, "git", "", map[string]any{"project_id": r.PathValue("projectID"), "conflicts": len(result.Conflicts)}, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]any{"conflicts": result.Conflicts})
	}
}

// gitPushHandler POST .../git/push — pushes the current branch, auto-setting
// its upstream (`push -u origin HEAD`) on a first push. Never forces.
func gitPushHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		result, ok := gitCommandTimeout(w, r, conn, protocol.GitCommandPayload{Action: "push", Root: root}, gitNetworkRPCTimeout)
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventGitPushed, audit.Actor{UserID: user.ID, Email: user.Email}, "git", "", map[string]any{"project_id": r.PathValue("projectID")}, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]bool{"pushed_new_upstream": result.PushedNewUpstream})
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
