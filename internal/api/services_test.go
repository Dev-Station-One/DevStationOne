package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"devstation/internal/auth"
	"devstation/internal/protocol"
	"devstation/internal/services"
)

// testServiceUnit installs a small, disposable unit file under the real
// user's systemd --user directory and guarantees cleanup, mirroring
// internal/services/services_test.go's fixture (duplicated rather than
// shared across the package boundary test files can't cross).
func testServiceUnit(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if !services.Available(ctx) {
		t.Skip("systemd --user not available; skipping integration test")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("determine home dir: %v", err)
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	name := fmt.Sprintf("devstation-apitest-%d.service", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	unitFile := "[Unit]\nDescription=DevStation Slice 5 API test service\n\n" +
		"[Service]\nExecStart=/bin/sh -c 'while true; do sleep 1; done'\n\n" +
		"[Install]\nWantedBy=default.target\n"
	if err := os.WriteFile(path, []byte(unitFile), 0o644); err != nil {
		t.Fatalf("write unit file: %v", err)
	}
	if err := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run(); err != nil {
		t.Fatalf("daemon-reload: %v", err)
	}

	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "stop", name).Run()
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "disable", name).Run()
		_ = os.Remove(path)
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "daemon-reload").Run()
	})

	return name
}

func TestServiceHTTP_ListStatusStartStopEnableDisableLogs(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)
	unit := testServiceUnit(t)

	// list: our unit isn't running yet, so it may or may not appear
	// (list-units --all still shows loaded-but-inactive units once
	// daemon-reload has seen the file — either way this call must succeed).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// start
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/services/"+unit+"/start", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var info protocol.ServiceInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if info.ActiveState != "active" || info.SubState != "running" {
		t.Fatalf("after start: %+v, want active/running", info)
	}

	// status
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services/"+unit, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	json.NewDecoder(rec.Body).Decode(&info)
	if info.Unit != unit {
		t.Fatalf("status Unit = %q, want %q", info.Unit, unit)
	}

	// list now includes it, active
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var list []protocol.ServiceInfo
	json.NewDecoder(rec.Body).Decode(&list)
	found := false
	for _, u := range list {
		if u.Unit == unit {
			found = true
			if u.ActiveState != "active" {
				t.Fatalf("list entry for %s: ActiveState = %q, want active", unit, u.ActiveState)
			}
		}
	}
	if !found {
		t.Fatalf("list did not include %s: %+v", unit, list)
	}

	// enable / disable
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/services/"+unit+"/enable", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&info)
	if info.Enabled != "enabled" {
		t.Fatalf("after enable: Enabled = %q, want enabled", info.Enabled)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/services/"+unit+"/disable", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&info)
	if info.Enabled == "enabled" {
		t.Fatalf("after disable: Enabled = %q, want not enabled", info.Enabled)
	}

	// logs
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services/"+unit+"/logs?lines=50", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var logLines []string
	if err := json.NewDecoder(rec.Body).Decode(&logLines); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}

	// stop
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/services/"+unit+"/stop", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&info)
	if info.ActiveState == "active" {
		t.Fatalf("after stop: ActiveState = %q, want inactive", info.ActiveState)
	}
}

func TestServiceHTTP_InvalidUnitNameRejected(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services/not-a-unit", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid unit: status = %d, want 400", rec.Code)
	}
}

func TestServiceHTTP_UnknownUnitReturnsBadGateway(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)
	if !services.Available(context.Background()) {
		t.Skip("systemd --user not available; skipping integration test")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services/devstation-definitely-not-a-real-unit.service", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unknown unit: status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServiceHTTP_OwnershipEnforced(t *testing.T) {
	router, _, _, projectID, db := sessionsTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services", nil)
	req.AddCookie(bobCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob list admin's project services: status = %d, want 404", rec.Code)
	}
}

func TestServiceHTTP_RuntimeOffline(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	// Revoke the runtime so its live connection drops, simulating offline.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var proj projectResponse
	json.NewDecoder(rec.Body).Decode(&proj)

	revokeReq := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+proj.RuntimeID+"/revoke", nil)
	revokeReq.AddCookie(cookie)
	revokeRec := httptest.NewRecorder()
	router.ServeHTTP(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d", revokeRec.Code)
	}

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var list []runtimeResponse
		json.NewDecoder(rec.Body).Decode(&list)
		for _, rt := range list {
			if rt.ID == proj.RuntimeID {
				return !rt.Online
			}
		}
		return false
	})

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/services", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("list with runtime offline: status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}
