package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"devstationone/internal/auth"
)

func TestAuditHTTP_ListRequiresAdminAndReflectsEvents(t *testing.T) {
	router, _, adminCookie, projectID, db := sessionsTestSetup(t)
	_ = projectID

	if _, err := auth.CreateUser(t.Context(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.AddCookie(bobCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: status = %d, want 403", rec.Code)
	}

	// sessionsTestSetup already performed a login, a runtime registration,
	// and a project creation — each of which must have recorded an event.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Events []auditEventResponse `json:"events"`
		Total  int                  `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range body.Events {
		seen[e.EventType] = true
	}
	for _, want := range []string{"auth.login", "runtime.registered", "project.created"} {
		if !seen[want] {
			t.Errorf("missing event type %q in %+v", want, body.Events)
		}
	}
	if body.Total != len(body.Events) {
		t.Fatalf("total = %d, len(events) = %d", body.Total, len(body.Events))
	}

	// Filter by event_type narrows the result to exactly that type.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit?event_type=project.created", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	body.Events = nil
	json.NewDecoder(rec.Body).Decode(&body)
	if body.Total != 1 {
		t.Fatalf("filtered total = %d, want 1", body.Total)
	}
	if body.Events[0].EventType != "project.created" {
		t.Fatalf("filtered event type = %q, want project.created", body.Events[0].EventType)
	}
}

func TestAuditHTTP_Settings(t *testing.T) {
	router, _, adminCookie, _, db := sessionsTestSetup(t)

	if _, err := auth.CreateUser(t.Context(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/settings", nil)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var s auditSettingsResponse
	json.NewDecoder(rec.Body).Decode(&s)
	if s.RetentionDays != 90 {
		t.Fatalf("default RetentionDays = %d, want 90", s.RetentionDays)
	}

	putBody, _ := json.Marshal(map[string]int{"retention_days": 30})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/audit/settings", bytes.NewReader(putBody))
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin update settings: status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/audit/settings", bytes.NewReader(putBody))
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update settings: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	json.NewDecoder(rec.Body).Decode(&s)
	if s.RetentionDays != 30 {
		t.Fatalf("RetentionDays after update = %d, want 30", s.RetentionDays)
	}
}
