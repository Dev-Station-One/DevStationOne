package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"devstationone/internal/auth"
)

// TestUserHTTP_DeleteWithHistoryCovers the ADR-following user-delete
// behavior: history rows (terminal_sessions, journal_notes) created by the
// user no longer block deletion (migration 0022, ON DELETE SET NULL), while
// active resources (backup_schedules, database_connections, projects,
// runtimes) still do — with the 400 error naming what's in the way.
func TestUserHTTP_DeleteWithHistory(t *testing.T) {
	router, adminCookie, db := usersTestSetup(t)
	pool := db.Pool

	target, err := auth.CreateUser(t.Context(), pool, "history@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	// A runtime + project owned by the admin (not the target) so the target
	// owns no active resources except the backup schedule below.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO runtimes (name, token_hash, created_by) VALUES ('rt-hist', 'h-hist', $1)`, target.ID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	var projectID string
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		 SELECT 'p-hist', id, '/', '/bin/bash', '{}', $1 FROM runtimes WHERE token_hash = 'h-hist'
		 RETURNING id`, target.ID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE projects SET created_by = (SELECT id FROM users WHERE is_admin AND id <> $1 LIMIT 1) WHERE id = $2`, target.ID, projectID); err != nil {
		t.Fatalf("reassign project: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE runtimes SET created_by = (SELECT id FROM users WHERE is_admin AND id <> $1 LIMIT 1) WHERE token_hash = 'h-hist'`, target.ID); err != nil {
		t.Fatalf("reassign runtime: %v", err)
	}

	// History rows owned by the target.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO terminal_sessions (project_id, name, cols, rows, created_by)
		 VALUES ($1, 's1', 80, 24, $2)`, projectID, target.ID); err != nil {
		t.Fatalf("create terminal session: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO journal_notes (project_id, body_html, created_by)
		 VALUES ($1, 'note by target', $2)`, projectID, target.ID); err != nil {
		t.Fatalf("create journal note: %v", err)
	}

	// Active resource the target also owns: blocks deletion, named in the error.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO backup_schedules (kind, project_id, cadence, retention, next_run_at, created_by)
		 VALUES ('project', $1, 'daily', 3, $2, $3)`, projectID, time.Now().Add(time.Hour), target.ID); err != nil {
		t.Fatalf("create backup schedule: %v", err)
	}

	rec := adminReq(t, router, adminCookie, http.MethodDelete, "/api/v1/users/"+target.ID, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete with backup schedule: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var errResp map[string]string
	json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp["error"] != "user still owns resources: delete or reassign their backup schedules first" {
		t.Fatalf("ownership error = %q", errResp["error"])
	}

	// Clear the blocking resource; history rows must not block the delete.
	if _, err := pool.Exec(t.Context(), `DELETE FROM backup_schedules WHERE created_by = $1`, target.ID); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}

	rec = adminReq(t, router, adminCookie, http.MethodDelete, "/api/v1/users/"+target.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete with history rows: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var creator *string
	if err := pool.QueryRow(t.Context(),
		`SELECT created_by FROM journal_notes WHERE project_id = $1`, projectID).Scan(&creator); err != nil {
		t.Fatalf("journal note after user delete: %v", err)
	}
	if creator != nil {
		t.Fatalf("journal note created_by = %s, want NULL (ON DELETE SET NULL)", *creator)
	}
}
