package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"devstationone/internal/agent"
	"devstationone/internal/testdb"
)

// withTestBackupsRoot points the real runtime-side backup manager at a temp
// directory for the duration of a test, instead of the real
// ~/.local/share/devstationone/backups — these tests run a real fake
// runtime (agent.NewManager) in-process, and its backup manager must not
// write into the developer's actual home directory.
func withTestBackupsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	orig := agent.BackupsRoot
	agent.BackupsRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { agent.BackupsRoot = orig })
	return root
}

// awaitBackupDone polls GET .../backups/{id} until state is no longer
// "running", returning the final backupResponse.
func awaitBackupDone(t *testing.T, router http.Handler, cookie *http.Cookie, getURL string) backupResponse {
	t.Helper()
	var b backupResponse
	pollUntil(t, 30*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, getURL, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}
		json.NewDecoder(rec.Body).Decode(&b)
		return b.State != "running"
	})
	return b
}

func TestDatabaseBackupHTTP_CreateListDownloadRestoreDelete(t *testing.T) {
	withTestBackupsRoot(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	createBody, _ := json.Marshal(databaseConnectionRequest{
		Name: "primary", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase,
		SSLMode: "disable",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(createBody))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var conn databaseConnectionResponse
	json.NewDecoder(rec.Body).Decode(&conn)
	base := "/api/v1/projects/" + projectID + "/databases/" + conn.ID

	// Back up now.
	req = httptest.NewRequest(http.MethodPost, base+"/backups", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create backup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var b backupResponse
	json.NewDecoder(rec.Body).Decode(&b)

	b = awaitBackupDone(t, router, cookie, base+"/backups/"+b.ID)
	if b.State != "done" {
		t.Fatalf("backup did not complete: state = %q, error = %q", b.State, b.Error)
	}
	if b.SizeBytes == 0 {
		t.Error("SizeBytes = 0, want > 0 for a real pg_dump")
	}

	// List includes it.
	req = httptest.NewRequest(http.MethodGet, base+"/backups", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var list []backupResponse
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("list = %+v", list)
	}

	// Download streams the real file back.
	req = httptest.NewRequest(http.MethodGet, base+"/backups/"+b.ID+"/download", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status = %d", rec.Code)
	}
	if int64(rec.Body.Len()) != b.SizeBytes {
		t.Errorf("downloaded %d bytes, want %d", rec.Body.Len(), b.SizeBytes)
	}

	// Restore requires confirm=true.
	restoreBody, _ := json.Marshal(restoreDBRequest{TargetDatabase: "restored_db", Confirm: false})
	req = httptest.NewRequest(http.MethodPost, base+"/backups/"+b.ID+"/restore", bytes.NewReader(restoreBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("restore without confirm: status = %d, want 400", rec.Code)
	}

	// Restore into a new database, confirmed.
	restoreBody, _ = json.Marshal(restoreDBRequest{TargetDatabase: "restored_db", Confirm: true})
	req = httptest.NewRequest(http.MethodPost, base+"/backups/"+b.ID+"/restore", bytes.NewReader(restoreBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() { dropTestDatabase(t, "restored_db") })

	// Delete removes both the runtime file and the metadata row.
	req = httptest.NewRequest(http.MethodDelete, base+"/backups/"+b.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, base+"/backups/"+b.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want 404", rec.Code)
	}
}

// dropTestDatabase drops a database created by a restore test, using the
// same test Postgres container as testdb.Postgres*. Best-effort: a stray
// test database left behind is harmless (the test container is itself
// ephemeral, ADR-031), so failures here are not fatal.
func dropTestDatabase(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("dropdb",
		"--host="+testdb.PostgresHost, "--port="+strconv.Itoa(testdb.PostgresPort),
		"--username="+testdb.PostgresUser, name)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+testdb.PostgresPassword)
	_ = cmd.Run()
}

func TestDatabaseBackupHTTP_SQLiteRoundTrip(t *testing.T) {
	withTestBackupsRoot(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	dbPath := filepath.Join(t.TempDir(), "app.db")
	if err := exec.Command("sqlite3", dbPath, "CREATE TABLE t(x INTEGER); INSERT INTO t VALUES (1);").Run(); err != nil {
		t.Fatalf("seed sqlite db: %v", err)
	}

	createBody, _ := json.Marshal(databaseConnectionRequest{
		Name: "sqlite-primary", Engine: "sqlite", Database: dbPath,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(createBody))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var conn databaseConnectionResponse
	json.NewDecoder(rec.Body).Decode(&conn)
	base := "/api/v1/projects/" + projectID + "/databases/" + conn.ID

	req = httptest.NewRequest(http.MethodPost, base+"/backups", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create backup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var b backupResponse
	json.NewDecoder(rec.Body).Decode(&b)
	b = awaitBackupDone(t, router, cookie, base+"/backups/"+b.ID)
	if b.State != "done" {
		t.Fatalf("sqlite backup did not complete: state = %q, error = %q", b.State, b.Error)
	}
	if b.SizeBytes == 0 {
		t.Error("SizeBytes = 0, want > 0 for a real sqlite3 .backup")
	}

	restorePath := filepath.Join(t.TempDir(), "restored.db")
	restoreBody, _ := json.Marshal(restoreDBRequest{TargetDatabase: restorePath, Confirm: true})
	req = httptest.NewRequest(http.MethodPost, base+"/backups/"+b.ID+"/restore", bytes.NewReader(restoreBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sqlite restore: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	out, err := exec.Command("sqlite3", restorePath, "SELECT x FROM t;").CombinedOutput()
	if err != nil || string(bytes.TrimSpace(out)) != "1" {
		t.Fatalf("restored sqlite db content = %q, err = %v", out, err)
	}
}

func TestDatabaseBackupHTTP_ScheduleCRUD(t *testing.T) {
	withTestBackupsRoot(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	createBody, _ := json.Marshal(databaseConnectionRequest{
		Name: "primary", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase,
		SSLMode: "disable",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(createBody))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var conn databaseConnectionResponse
	json.NewDecoder(rec.Body).Decode(&conn)
	base := "/api/v1/projects/" + projectID + "/databases/" + conn.ID + "/backups/schedule"

	// No schedule yet.
	req = httptest.NewRequest(http.MethodGet, base, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get before create: status = %d, want 404", rec.Code)
	}

	// Invalid cadence rejected.
	badBody, _ := json.Marshal(scheduleRequest{Cadence: "monthly", Retention: 5})
	req = httptest.NewRequest(http.MethodPut, base, bytes.NewReader(badBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("put invalid cadence: status = %d, want 400", rec.Code)
	}

	// Create.
	body, _ := json.Marshal(scheduleRequest{Cadence: "daily", Retention: 5})
	req = httptest.NewRequest(http.MethodPut, base, bytes.NewReader(body))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var s scheduleResponse
	json.NewDecoder(rec.Body).Decode(&s)
	if s.Cadence != "daily" || s.Retention != 5 {
		t.Fatalf("schedule = %+v", s)
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, base, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", rec.Code)
	}
}

func TestProjectBackupHTTP_CreateRestoreAsNewOverwriteDelete(t *testing.T) {
	withTestBackupsRoot(t)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "app.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	router, _, cookie, projectID, _ := sessionsTestSetupIn(t, workDir)

	// Back up now.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/backups", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create backup: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var b backupResponse
	json.NewDecoder(rec.Body).Decode(&b)
	b = awaitBackupDone(t, router, cookie, "/api/v1/projects/"+projectID+"/backups/"+b.ID)
	if b.State != "done" {
		t.Fatalf("backup did not complete: state = %q, error = %q", b.State, b.Error)
	}

	// Restore as new project.
	restoreBody, _ := json.Marshal(map[string]string{"mode": "new"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/backups/"+b.ID+"/restore", bytes.NewReader(restoreBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("restore as new: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var newResp struct {
		ProjectID        string `json:"project_id"`
		WorkingDirectory string `json:"working_directory"`
	}
	json.NewDecoder(rec.Body).Decode(&newResp)
	if _, err := os.Stat(filepath.Join(newResp.WorkingDirectory, "app.txt")); err != nil {
		t.Errorf("restored project missing app.txt: %v", err)
	}

	// Overwrite-restore requires exact confirm_name.
	overwriteBody, _ := json.Marshal(map[string]string{"mode": "overwrite", "confirm_name": "wrong name"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/backups/"+b.ID+"/restore", bytes.NewReader(overwriteBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("overwrite with wrong name: status = %d, want 400", rec.Code)
	}

	// Modify the working directory, then overwrite-restore with the
	// correct name and confirm the original content came back.
	if err := os.WriteFile(filepath.Join(workDir, "app.txt"), []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil)
	getReq.AddCookie(cookie)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	var proj projectResponse
	json.NewDecoder(getRec.Body).Decode(&proj)

	overwriteBody, _ = json.Marshal(map[string]string{"mode": "overwrite", "confirm_name": proj.Name})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/backups/"+b.ID+"/restore", bytes.NewReader(overwriteBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite restore: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(workDir, "app.txt"))
	if err != nil || string(data) != "v1" {
		t.Errorf("app.txt after overwrite-restore = %q, %v, want %q", data, err, "v1")
	}

	// A pre_restore safety snapshot was recorded.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/backups", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var list []backupResponse
	json.NewDecoder(rec.Body).Decode(&list)
	foundSnapshot := false
	for _, row := range list {
		if row.Trigger == "pre_restore" {
			foundSnapshot = true
		}
	}
	if !foundSnapshot {
		t.Errorf("no pre_restore snapshot recorded: %+v", list)
	}

	// Delete the original manual backup.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID+"/backups/"+b.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
