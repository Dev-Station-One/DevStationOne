package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
)

// adminUserResponse is an account as the user-manager sees it; password
// material is never included.
type adminUserResponse struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	DisplayName    string     `json:"display_name"`
	Role           string     `json:"role"`
	LastAccessedAt *time.Time `json:"last_accessed_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toAdminUserResponse(u auth.AdminUser) adminUserResponse {
	role := "user"
	if u.IsAdmin {
		role = "admin"
	}
	return adminUserResponse{
		ID:             u.ID,
		Email:          u.Email,
		DisplayName:    u.DisplayName,
		Role:           role,
		LastAccessedAt: u.LastAccessedAt,
		CreatedAt:      u.CreatedAt,
	}
}

type createUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type patchUserRequest struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	Role        *string `json:"role"`
}

// validateAdminPassword rejects empty or too-short passwords at the
// trust boundary — a weak admin-created account is a weak account.
func validateAdminPassword(p string) string {
	if len(p) < 8 {
		return "password must be at least 8 characters"
	}
	return ""
}

func listUsersHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := auth.ListAdminUsers(r.Context(), db.Pool)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
			return
		}
		resp := make([]adminUserResponse, 0, len(list))
		for _, u := range list {
			resp = append(resp, toAdminUserResponse(u))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createUserHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r.Context())

		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.DisplayName = strings.TrimSpace(req.DisplayName)
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if msg := validateAdminPassword(req.Password); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		isAdmin := req.Role == "admin"
		if req.Role != "" && req.Role != "admin" && req.Role != "user" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'user' or 'admin'"})
			return
		}

		u, err := auth.CreateUser(r.Context(), db.Pool, req.Email, req.Password, isAdmin)
		if err == auth.ErrEmailTaken {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
			return
		}
		if req.DisplayName != "" {
			// CreateUser's 37 existing callers have no display_name param;
			// the setter covers it.
			_ = auth.SetDisplayName(r.Context(), db.Pool, u.ID, req.DisplayName)
			u.DisplayName = req.DisplayName
		}

		audit.Record(r.Context(), db.Pool, audit.EventUserCreated, audit.Actor{UserID: admin.ID, Email: admin.Email}, "user", u.ID, map[string]any{"email": u.Email}, clientIP(r))
		writeJSON(w, http.StatusCreated, toAdminUserResponse(auth.AdminUser{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, CreatedAt: u.CreatedAt,
		}))
	}
}

// guardAdminEdit returns a client-facing error when admin would lock
// everyone out: demoting or deleting self, or demoting/deleting the last
// remaining admin. "" means the action may proceed.
func guardAdminEdit(r *http.Request, db *database.DB, target auth.User, demoting, deleting bool) string {
	admin, _ := userFromContext(r.Context())
	if deleting || demoting {
		if target.ID == admin.ID {
			return "you cannot demote or delete your own account"
		}
	}
	if demoting || deleting {
		if target.IsAdmin {
			n, err := auth.CountAdmins(r.Context(), db.Pool)
			if err != nil {
				return "failed to count administrators"
			}
			if n <= 1 {
				return "cannot demote or delete the last administrator"
			}
		}
	}
	return ""
}

func patchUserHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r.Context())
		id := r.PathValue("userID")

		existing, err := auth.GetUserByID(r.Context(), db.Pool, id)
		if err == auth.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up user"})
			return
		}

		var req patchUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.DisplayName == nil && req.Password == nil && req.Role == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one field is required"})
			return
		}
		if req.Password != nil {
			if msg := validateAdminPassword(*req.Password); msg != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
				return
			}
		}
		var rolePtr *bool
		demoting := false
		if req.Role != nil {
			if *req.Role != "user" && *req.Role != "admin" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'user' or 'admin'"})
				return
			}
			v := *req.Role == "admin"
			rolePtr = &v
			demoting = existing.IsAdmin && !v
			if msg := guardAdminEdit(r, db, existing, demoting, false); msg != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
				return
			}
		}

		u, err := auth.AdminUpdate(r.Context(), db.Pool, id, req.DisplayName, req.Password, rolePtr)
		if err == auth.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update user"})
			return
		}

		audit.Record(r.Context(), db.Pool, audit.EventUserUpdated, audit.Actor{UserID: admin.ID, Email: admin.Email}, "user", u.ID, map[string]any{"email": u.Email}, clientIP(r))
		writeJSON(w, http.StatusOK, toAdminUserResponse(auth.AdminUser{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, CreatedAt: u.CreatedAt,
		}))
	}
}

func deleteUserHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r.Context())
		id := r.PathValue("userID")

		existing, err := auth.GetUserByID(r.Context(), db.Pool, id)
		if err == auth.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up user"})
			return
		}

		if msg := guardAdminEdit(r, db, existing, false, true); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		// Ownership blocks the FK: the caller cleans up first, and the
		// error names what's in the way. History rows (terminal_sessions,
		// journal_notes, project_commands, command_runs, backups) no longer
		// block — migration 0022 made those FKs ON DELETE SET NULL.
		var projects, runtimes, backupSchedules, dbConnections int
		if err := db.Pool.QueryRow(r.Context(),
			`SELECT (SELECT count(*) FROM projects             WHERE created_by = $1),
			        (SELECT count(*) FROM runtimes              WHERE created_by = $1),
			        (SELECT count(*) FROM backup_schedules      WHERE created_by = $1),
			        (SELECT count(*) FROM database_connections  WHERE created_by = $1)`, id,
		).Scan(&projects, &runtimes, &backupSchedules, &dbConnections); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check ownership"})
			return
		}
		var owned []string
		if projects > 0 {
			owned = append(owned, "projects")
		}
		if runtimes > 0 {
			owned = append(owned, "runtimes")
		}
		if backupSchedules > 0 {
			owned = append(owned, "backup schedules")
		}
		if dbConnections > 0 {
			owned = append(owned, "database connections")
		}
		if len(owned) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "user still owns resources: delete or reassign their " + strings.Join(owned, ", ") + " first",
			})
			return
		}

		ok, err := auth.DeleteUser(r.Context(), db.Pool, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete user"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}

		audit.Record(r.Context(), db.Pool, audit.EventUserDeleted, audit.Actor{UserID: admin.ID, Email: admin.Email}, "user", id, map[string]any{"email": existing.Email}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

type userResources struct {
	Projects []ownedResource `json:"projects"`
	Runtimes []ownedResource `json:"runtimes"`
}

type ownedResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func getUserResourcesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.GetUserByID(r.Context(), db.Pool, r.PathValue("userID")); err == auth.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		} else if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up user"})
			return
		}

		var out userResources
		if err := db.Pool.QueryRow(r.Context(),
			`SELECT COALESCE((SELECT json_agg(json_build_object('id', id, 'name', name) ORDER BY name) FROM projects WHERE created_by = $1), '[]'::json),
			        COALESCE((SELECT json_agg(json_build_object('id', id, 'name', name) ORDER BY name) FROM runtimes  WHERE created_by = $1), '[]'::json)`,
			r.PathValue("userID"),
		).Scan(&out.Projects, &out.Runtimes); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up resources"})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type reassignRequest struct {
	TargetUserID string `json:"target_user_id"`
	// ProjectIDs/RuntimeIDs: nil (absent) = leave that kind alone; an
	// empty array = everything of that kind; a non-empty array = those IDs.
	ProjectIDs *[]string `json:"project_ids"`
	RuntimeIDs *[]string `json:"runtime_ids"`
}

// reassignUserResourcesHandler moves ownership of projects/runtimes from
// one user to another. An omitted or empty ID list means "everything that
// kind"; a runtime transfer only changes created_by — enrollment and
// connection are untouched.
func reassignUserResourcesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r.Context())
		sourceID := r.PathValue("userID")

		var req reassignRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.TargetUserID == "" || req.TargetUserID == sourceID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_user_id must name a different user"})
			return
		}
		target, err := auth.GetUserByID(r.Context(), db.Pool, req.TargetUserID)
		if err == auth.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up target user"})
			return
		}

		transfer := func(table string, ids []string) (int64, error) {
			var tag pgconn.CommandTag
			var err error
			if len(ids) == 0 {
				tag, err = db.Pool.Exec(r.Context(),
					`UPDATE `+table+` SET created_by = $1 WHERE created_by = $2`, target.ID, sourceID)
			} else {
				tag, err = db.Pool.Exec(r.Context(),
					`UPDATE `+table+` SET created_by = $1 WHERE created_by = $2 AND id = ANY($3::uuid[])`,
					target.ID, sourceID, ids)
			}
			if err != nil {
				return 0, err
			}
			return tag.RowsAffected(), nil
		}

		var movedProjects, movedRuntimes int64
		if req.ProjectIDs != nil {
			movedProjects, err = transfer("projects", *req.ProjectIDs)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reassign projects"})
				return
			}
		}
		if req.RuntimeIDs != nil {
			movedRuntimes, err = transfer("runtimes", *req.RuntimeIDs)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reassign runtimes"})
				return
			}
		}

		audit.Record(r.Context(), db.Pool, audit.EventOwnershipTransferred, audit.Actor{UserID: admin.ID, Email: admin.Email},
			"user", sourceID,
			map[string]any{"target_user_id": target.ID, "projects": movedProjects, "runtimes": movedRuntimes},
			clientIP(r))
		writeJSON(w, http.StatusOK, map[string]int{"projects": int(movedProjects), "runtimes": int(movedRuntimes)})
	}
}
