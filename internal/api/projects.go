package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

type projectRequest struct {
	Name             string            `json:"name"`
	RuntimeID        string            `json:"runtime_id"`
	WorkingDirectory string            `json:"working_directory"`
	Shell            string            `json:"shell"`
	Environment      map[string]string `json:"environment"`
}

// projectPatchRequest mirrors projectRequest but with optional fields, so a
// PATCH only touches the properties the client actually sent.
type projectPatchRequest struct {
	Name             *string            `json:"name"`
	RuntimeID        *string            `json:"runtime_id"`
	WorkingDirectory *string            `json:"working_directory"`
	Shell            *string            `json:"shell"`
	Environment      *map[string]string `json:"environment"`
}

type projectResponse struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	RuntimeID        string            `json:"runtime_id"`
	WorkingDirectory string            `json:"working_directory"`
	Shell            string            `json:"shell"`
	Environment      map[string]string `json:"environment"`
	CreatedBy        string            `json:"created_by"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ActiveSessions   int               `json:"active_sessions"`
}

func toProjectResponse(p projects.Project, activeSessions int) projectResponse {
	env := p.Environment
	if env == nil {
		env = map[string]string{}
	}
	return projectResponse{
		ID:               p.ID,
		Name:             p.Name,
		RuntimeID:        p.RuntimeID,
		WorkingDirectory: p.WorkingDirectory,
		Shell:            p.Shell,
		Environment:      env,
		CreatedBy:        p.CreatedBy,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
		ActiveSessions:   activeSessions,
	}
}

// activeSessionCount looks up how many non-terminal sessions projectID
// currently has, for enriching a single project's response (dashboard.
// active_sessions — docs/IMPLEMENTATION_PLAN.md Slice 14).
func activeSessionCount(ctx context.Context, pool *pgxpool.Pool, projectID string) (int, error) {
	counts, err := sessions.ActiveCountsByProjectIDs(ctx, pool, []string{projectID})
	if err != nil {
		return 0, err
	}
	return counts[projectID], nil
}

// validateProjectFields checks the required, non-runtime-existence properties
// of a project and returns a client-facing error message, or "" if valid.
func validateProjectFields(f projects.Fields) string {
	switch {
	case strings.TrimSpace(f.Name) == "":
		return "name is required"
	case strings.TrimSpace(f.RuntimeID) == "":
		return "runtime_id is required"
	case strings.TrimSpace(f.WorkingDirectory) == "":
		return "working_directory is required"
	case !strings.HasPrefix(f.WorkingDirectory, "/"):
		return "working_directory must be an absolute path"
	case strings.TrimSpace(f.Shell) == "":
		return "shell is required"
	case !strings.HasPrefix(f.Shell, "/"):
		return "shell must be an absolute path"
	}
	return ""
}

// resolveRuntime confirms runtimeID names an existing, non-revoked, online
// runtime that has reported a home directory, and returns its live
// connection alongside its record. Runtime IDs are client-supplied and must
// never be trusted without a server-side lookup; a Project can only be
// created or edited while its runtime is reachable, since setup now depends
// on live runtime state (available shells, home directory) rather than
// client-typed values.
func resolveRuntime(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, runtimeID string) (runtimes.Runtime, runtimes.Conn, bool) {
	rt, err := runtimes.GetByID(r.Context(), db.Pool, runtimeID)
	if errors.Is(err, runtimes.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime not found"})
		return runtimes.Runtime{}, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up runtime"})
		return runtimes.Runtime{}, nil, false
	}
	if rt.Status == runtimes.StatusRevoked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime is revoked"})
		return runtimes.Runtime{}, nil, false
	}
	if rt.Capabilities.HomeDir == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime has not reported a home directory yet"})
		return runtimes.Runtime{}, nil, false
	}
	conn, online := registry.Get(runtimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
		return runtimes.Runtime{}, nil, false
	}
	return rt, conn, true
}

// validateShell confirms shell is one the runtime actually reported (parsed
// from /etc/shells) — a client must not be able to type an arbitrary path.
func validateShell(w http.ResponseWriter, rt runtimes.Runtime, shell string) bool {
	if slices.Contains(rt.Capabilities.Shells, shell) {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shell is not available on this runtime"})
	return false
}

// validateWorkingDirectory confirms dir is contained within the runtime's
// home directory and really exists there. The Rel check is a cheap early
// rejection; the "stat" RPC is the real check, re-using the same os.Root
// containment (internal/filesystem) the runtime already enforces for file
// operations — no second path-safety implementation.
func validateWorkingDirectory(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, homeDir, dir string) bool {
	rel, err := filepath.Rel(homeDir, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "working_directory must be inside the runtime's home directory"})
		return false
	}
	result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "stat", Root: homeDir, Path: rel})
	if !ok {
		return false
	}
	if result.Info == nil || result.Info.Type != "dir" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "working_directory does not exist"})
		return false
	}
	return true
}

func createProjectHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())

		var req projectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		fields := projects.Fields{
			Name:             req.Name,
			RuntimeID:        req.RuntimeID,
			WorkingDirectory: req.WorkingDirectory,
			Shell:            req.Shell,
			Environment:      req.Environment,
		}
		if msg := validateProjectFields(fields); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		rt, conn, ok := resolveRuntime(w, r, db, registry, fields.RuntimeID)
		if !ok {
			return
		}
		if !validateShell(w, rt, fields.Shell) {
			return
		}
		if !validateWorkingDirectory(w, r, conn, rt.Capabilities.HomeDir, fields.WorkingDirectory) {
			return
		}

		p, err := projects.Create(r.Context(), db.Pool, fields, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create project"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventProjectCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "project", p.ID, map[string]any{"name": p.Name}, clientIP(r))
		writeJSON(w, http.StatusCreated, toProjectResponse(p, 0))
	}
}

func listProjectsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())

		var (
			list []projects.Project
			err  error
		)
		if user.IsAdmin {
			list, err = projects.ListAll(r.Context(), db.Pool)
		} else {
			list, err = projects.ListForUser(r.Context(), db.Pool, user.ID)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list projects"})
			return
		}

		ids := make([]string, len(list))
		for i, p := range list {
			ids[i] = p.ID
		}
		counts, err := sessions.ActiveCountsByProjectIDs(r.Context(), db.Pool, ids)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list projects"})
			return
		}

		resp := make([]projectResponse, 0, len(list))
		for _, p := range list {
			resp = append(resp, toProjectResponse(p, counts[p.ID]))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// authorizedProject looks up a project and confirms the authenticated user
// may access it, writing a 404 (never 403, to avoid confirming another
// user's project exists) if not.
func authorizedProject(w http.ResponseWriter, r *http.Request, db *database.DB, projectID string) (projects.Project, bool) {
	user, _ := userFromContext(r.Context())

	p, err := projects.Get(r.Context(), db.Pool, projectID)
	if errors.Is(err, projects.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return projects.Project{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up project"})
		return projects.Project{}, false
	}
	if !user.IsAdmin && p.CreatedBy != user.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return projects.Project{}, false
	}
	return p, true
}

func getProjectHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		count, err := activeSessionCount(r.Context(), db.Pool, p.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up project"})
			return
		}
		writeJSON(w, http.StatusOK, toProjectResponse(p, count))
	}
}

func patchProjectHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}

		var req projectPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		fields := projects.Fields{
			Name:             existing.Name,
			RuntimeID:        existing.RuntimeID,
			WorkingDirectory: existing.WorkingDirectory,
			Shell:            existing.Shell,
			Environment:      existing.Environment,
		}
		if req.Name != nil {
			fields.Name = *req.Name
		}
		if req.RuntimeID != nil {
			fields.RuntimeID = *req.RuntimeID
		}
		if req.WorkingDirectory != nil {
			fields.WorkingDirectory = *req.WorkingDirectory
		}
		if req.Shell != nil {
			fields.Shell = *req.Shell
		}
		if req.Environment != nil {
			fields.Environment = *req.Environment
		}

		if msg := validateProjectFields(fields); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		// Re-validated on every patch, not just when runtime_id/shell/
		// working_directory change: the runtime-offline gate must hold for
		// any edit, not only ones that touch runtime-derived fields.
		rt, conn, ok := resolveRuntime(w, r, db, registry, fields.RuntimeID)
		if !ok {
			return
		}
		if !validateShell(w, rt, fields.Shell) {
			return
		}
		if !validateWorkingDirectory(w, r, conn, rt.Capabilities.HomeDir, fields.WorkingDirectory) {
			return
		}

		p, err := projects.Update(r.Context(), db.Pool, existing.ID, fields)
		if errors.Is(err, projects.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update project"})
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventProjectUpdated, audit.Actor{UserID: user.ID, Email: user.Email}, "project", p.ID, map[string]any{"name": p.Name}, clientIP(r))
		count, err := activeSessionCount(r.Context(), db.Pool, p.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update project"})
			return
		}
		writeJSON(w, http.StatusOK, toProjectResponse(p, count))
	}
}

func deleteProjectHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}

		ok, err := projects.Delete(r.Context(), db.Pool, r.PathValue("projectID"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete project"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventProjectDeleted, audit.Actor{UserID: user.ID, Email: user.Email}, "project", existing.ID, map[string]any{"name": existing.Name}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}
