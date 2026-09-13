package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"devstation/internal/database"
	"devstation/internal/projects"
	"devstation/internal/runtimes"
)

type projectRequest struct {
	Name             string            `json:"name"`
	RuntimeID        string            `json:"runtime_id"`
	OSUser           string            `json:"os_user"`
	WorkingDirectory string            `json:"working_directory"`
	Shell            string            `json:"shell"`
	Environment      map[string]string `json:"environment"`
}

// projectPatchRequest mirrors projectRequest but with optional fields, so a
// PATCH only touches the properties the client actually sent.
type projectPatchRequest struct {
	Name             *string            `json:"name"`
	RuntimeID        *string            `json:"runtime_id"`
	OSUser           *string            `json:"os_user"`
	WorkingDirectory *string            `json:"working_directory"`
	Shell            *string            `json:"shell"`
	Environment      *map[string]string `json:"environment"`
}

type projectResponse struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	RuntimeID        string            `json:"runtime_id"`
	OSUser           string            `json:"os_user"`
	WorkingDirectory string            `json:"working_directory"`
	Shell            string            `json:"shell"`
	Environment      map[string]string `json:"environment"`
	CreatedBy        string            `json:"created_by"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func toProjectResponse(p projects.Project) projectResponse {
	env := p.Environment
	if env == nil {
		env = map[string]string{}
	}
	return projectResponse{
		ID:               p.ID,
		Name:             p.Name,
		RuntimeID:        p.RuntimeID,
		OSUser:           p.OSUser,
		WorkingDirectory: p.WorkingDirectory,
		Shell:            p.Shell,
		Environment:      env,
		CreatedBy:        p.CreatedBy,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
	}
}

// validateProjectFields checks the required, non-runtime-existence properties
// of a project and returns a client-facing error message, or "" if valid.
func validateProjectFields(f projects.Fields) string {
	switch {
	case strings.TrimSpace(f.Name) == "":
		return "name is required"
	case strings.TrimSpace(f.RuntimeID) == "":
		return "runtime_id is required"
	case strings.TrimSpace(f.OSUser) == "":
		return "os_user is required"
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

// resolveRuntime confirms runtimeID names an existing, non-revoked runtime.
// Runtime IDs are client-supplied and must never be trusted without a
// server-side lookup.
func resolveRuntime(w http.ResponseWriter, db *database.DB, r *http.Request, runtimeID string) bool {
	rt, err := runtimes.GetByID(r.Context(), db.Pool, runtimeID)
	if errors.Is(err, runtimes.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime not found"})
		return false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up runtime"})
		return false
	}
	if rt.Status == runtimes.StatusRevoked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime is revoked"})
		return false
	}
	return true
}

func createProjectHandler(db *database.DB) http.HandlerFunc {
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
			OSUser:           req.OSUser,
			WorkingDirectory: req.WorkingDirectory,
			Shell:            req.Shell,
			Environment:      req.Environment,
		}
		if msg := validateProjectFields(fields); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		if !resolveRuntime(w, db, r, fields.RuntimeID) {
			return
		}

		p, err := projects.Create(r.Context(), db.Pool, fields, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create project"})
			return
		}
		writeJSON(w, http.StatusCreated, toProjectResponse(p))
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

		resp := make([]projectResponse, 0, len(list))
		for _, p := range list {
			resp = append(resp, toProjectResponse(p))
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
		writeJSON(w, http.StatusOK, toProjectResponse(p))
	}
}

func patchProjectHandler(db *database.DB) http.HandlerFunc {
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
			OSUser:           existing.OSUser,
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
		if req.OSUser != nil {
			fields.OSUser = *req.OSUser
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
		if fields.RuntimeID != existing.RuntimeID {
			if !resolveRuntime(w, db, r, fields.RuntimeID) {
				return
			}
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
		writeJSON(w, http.StatusOK, toProjectResponse(p))
	}
}

func deleteProjectHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorizedProject(w, r, db, r.PathValue("projectID")); !ok {
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
		w.WriteHeader(http.StatusNoContent)
	}
}
