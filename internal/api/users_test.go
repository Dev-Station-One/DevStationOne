package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
)

// usersTestSetup builds a router with one admin login, returning the
// router, that admin's cookie, and the DB.
func usersTestSetup(t *testing.T) (http.Handler, *http.Cookie, *database.DB) {
	t.Helper()
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey)
	if _, err := auth.CreateUser(t.Context(), db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return router, loginAs(t, router, "admin@example.com", "password123"), db
}

func adminReq(t *testing.T, router http.Handler, cookie *http.Cookie, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestUserHTTP_CRUD(t *testing.T) {
	router, adminCookie, db := usersTestSetup(t)

	// Non-admin is rejected with 403.
	if _, err := auth.CreateUser(t.Context(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bob := loginAs(t, router, "bob@example.com", "password123")
	if rec := adminReq(t, router, bob, http.MethodGet, "/api/v1/users", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: status = %d, want 403", rec.Code)
	}

	// Create with too-short password rejected.
	rec := adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users",
		[]byte(`{"email":"carol@example.com","password":"short"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: status = %d, want 400", rec.Code)
	}

	// Create carol as a regular user.
	rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users",
		[]byte(`{"email":"carol@example.com","password":"carolpass1","display_name":"Carol","role":"user"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create carol: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created adminUserResponse
	json.NewDecoder(rec.Body).Decode(&created)
	if created.Role != "user" || created.DisplayName != "Carol" {
		t.Fatalf("created = %+v, want role=user display_name=Carol", created)
	}
	if created.LastAccessedAt != nil {
		t.Fatalf("last_accessed_at for new user = %v, want nil", created.LastAccessedAt)
	}
	carolID := created.ID

	// Duplicate email rejected with 409.
	if rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users",
		[]byte(`{"email":"carol@example.com","password":"carolpass1"}`)); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate email: status = %d, want 409", rec.Code)
	}

	// Carol logging in gives her a last-accessed timestamp.
	loginAs(t, router, "carol@example.com", "carolpass1")
	rec = adminReq(t, router, adminCookie, http.MethodGet, "/api/v1/users", nil)
	var list []adminUserResponse
	json.NewDecoder(rec.Body).Decode(&list)
	for _, u := range list {
		if u.Email == "carol@example.com" && u.LastAccessedAt == nil {
			t.Fatal("carol last_accessed_at = nil after login, want set")
		}
	}

	// Guard: demoting the last admin (the seeded admin) is rejected.
	adminID := findUser(t, router, adminCookie, "admin@example.com")
	rec = adminReq(t, router, adminCookie, http.MethodPatch, "/api/v1/users/"+adminID,
		[]byte(`{"role":"user"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("demote last admin: status = %d, want 400", rec.Code)
	}

	// With two admins, demotion is allowed both ways.
	if rec = adminReq(t, router, adminCookie, http.MethodPatch, "/api/v1/users/"+carolID,
		[]byte(`{"role":"admin"}`)); rec.Code != http.StatusOK {
		t.Fatalf("promote carol: status = %d", rec.Code)
	}
	if rec = adminReq(t, router, adminCookie, http.MethodPatch, "/api/v1/users/"+carolID,
		[]byte(`{"role":"user"}`)); rec.Code != http.StatusOK {
		t.Fatalf("demote carol (2 admins): status = %d", rec.Code)
	}

	// Password reset: new value takes effect for login.
	if rec = adminReq(t, router, adminCookie, http.MethodPatch, "/api/v1/users/"+carolID,
		[]byte(`{"password":"newcarolpass"}`)); rec.Code != http.StatusOK {
		t.Fatalf("reset password: status = %d", rec.Code)
	}
	loginAs(t, router, "carol@example.com", "newcarolpass")

	// Guard: admin cannot delete self.
	if rec = adminReq(t, router, adminCookie, http.MethodDelete, "/api/v1/users/"+adminID, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete self: status = %d, want 400", rec.Code)
	}

	// Delete carol.
	if rec = adminReq(t, router, adminCookie, http.MethodDelete, "/api/v1/users/"+carolID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete carol: status = %d", rec.Code)
	}

	// Audit trail reflects the events.
	events, _, err := audit.List(t.Context(), db.Pool, audit.Filter{EventType: "user.created"})
	if err != nil || len(events) < 1 {
		t.Fatalf("audit user.created: %v, %d events", err, len(events))
	}
}

// findUser resolves a user's ID by email through the admin list endpoint.
func findUser(t *testing.T, router http.Handler, cookie *http.Cookie, email string) string {
	t.Helper()
	rec := adminReq(t, router, cookie, http.MethodGet, "/api/v1/users", nil)
	var list []adminUserResponse
	json.NewDecoder(rec.Body).Decode(&list)
	for _, u := range list {
		if u.Email == email {
			return u.ID
		}
	}
	t.Fatalf("user %q not in list", email)
	return ""
}

func TestUserHTTP_ResourcesAndReassign(t *testing.T) {
	router, adminCookie, db := usersTestSetup(t)

	// Source user owns a project and a runtime.
	var source, target auth.User
	var err error
	if source, err = auth.CreateUser(t.Context(), db.Pool, "owner@example.com", "password123", false); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if target, err = auth.CreateUser(t.Context(), db.Pool, "receiver@example.com", "password123", false); err != nil {
		t.Fatalf("create receiver: %v", err)
	}
	pool := db.Pool
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO runtimes (name, token_hash, created_by) VALUES ('rt1', 'h', $1)`, source.ID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		 SELECT 'p1', r.id, '/', '/bin/bash', '{}', $1 FROM runtimes r WHERE r.created_by = $1`, source.ID); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Resources listing is accurate.
	rec := adminReq(t, router, adminCookie, http.MethodGet, "/api/v1/users/"+source.ID+"/resources", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources: status = %d", rec.Code)
	}
	var res userResources
	json.NewDecoder(rec.Body).Decode(&res)
	if len(res.Projects) != 1 || len(res.Runtimes) != 1 || res.Projects[0].Name != "p1" || res.Runtimes[0].Name != "rt1" {
		t.Fatalf("resources = %+v", res)
	}

	// Guards: missing target, target = source.
	if rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users/"+source.ID+"/reassign",
		[]byte(`{"target_user_id":"00000000-0000-0000-0000-000000000000"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown target: status = %d, want 404", rec.Code)
	}
	if rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users/"+source.ID+"/reassign",
		[]byte(`{"target_user_id":"`+source.ID+`"}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("self target: status = %d, want 400", rec.Code)
	}

	// Per-resource transfer: move just the runtime.
	if rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users/"+source.ID+"/reassign",
		[]byte(`{"target_user_id":"`+target.ID+`","runtime_ids":["`+res.Runtimes[0].ID+`"]}`)); rec.Code != http.StatusOK {
		t.Fatalf("reassign runtime: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = adminReq(t, router, adminCookie, http.MethodGet, "/api/v1/users/"+source.ID+"/resources", nil)
	json.NewDecoder(rec.Body).Decode(&res)
	if len(res.Projects) != 1 || len(res.Runtimes) != 0 {
		t.Fatalf("after runtime reassign = %+v, want 1 project 0 runtimes", res)
	}

	// Bulk: move everything else (empty arrays = all of that kind).
	if rec = adminReq(t, router, adminCookie, http.MethodPost, "/api/v1/users/"+source.ID+"/reassign",
		[]byte(`{"target_user_id":"`+target.ID+`","project_ids":[],"runtime_ids":[]}`)); rec.Code != http.StatusOK {
		t.Fatalf("reassign all: status = %d", rec.Code)
	}
	rec = adminReq(t, router, adminCookie, http.MethodGet, "/api/v1/users/"+source.ID+"/resources", nil)
	json.NewDecoder(rec.Body).Decode(&res)
	if len(res.Projects) != 0 || len(res.Runtimes) != 0 {
		t.Fatalf("after bulk reassign = %+v, want empty", res)
	}

	// Ownership cleared: the user is now deletable.
	if rec = adminReq(t, router, adminCookie, http.MethodDelete, "/api/v1/users/"+source.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete after reassign: status = %d", rec.Code)
	}

	// Audit trail recorded the transfers.
	events, _, err := audit.List(t.Context(), pool, audit.Filter{EventType: "ownership.transferred"})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit ownership.transferred: %v, %d events, want 2", err, len(events))
	}
}
