package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"devstationone/internal/testdb"
)

func TestDatabaseConnectionHTTP_CRUDAndTest(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	// Create
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
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created databaseConnectionResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.HasSecret {
		t.Error("HasSecret = false, want true")
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/databases", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var list []databaseConnectionResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil || len(list) != 1 {
		t.Fatalf("list: status = %d, body = %s, err = %v", rec.Code, rec.Body.String(), err)
	}

	// Test connection — real RPC to the fake runtime, which really opens
	// and pings the real Postgres test database (make db-up).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases/"+created.ID+"/test", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var testResult struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&testResult); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	if !testResult.Ok {
		t.Fatalf("test connection failed: %s (is `make db-up` running?)", testResult.Error)
	}

	// Patch: rename only, no secret — password must survive.
	patchBody, _ := json.Marshal(databaseConnectionPatchRequest{Name: strPtr("renamed")})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID+"/databases/"+created.ID, bytes.NewReader(patchBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched databaseConnectionResponse
	if err := json.NewDecoder(rec.Body).Decode(&patched); err != nil || patched.Name != "renamed" || !patched.HasSecret {
		t.Fatalf("patch result = %+v, err = %v, want name=renamed has_secret=true", patched, err)
	}

	// Test again after the secret-less rename — must still succeed.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases/"+created.ID+"/test", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&testResult)
	if !testResult.Ok {
		t.Fatalf("test after rename failed: %s", testResult.Error)
	}

	// Wrong password must produce ok=false, not a 500.
	badBody, _ := json.Marshal(databaseConnectionRequest{
		Name: "bad", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: "definitely-wrong", Database: testdb.PostgresDatabase, SSLMode: "disable",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(badBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var bad databaseConnectionResponse
	json.NewDecoder(rec.Body).Decode(&bad)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases/"+bad.ID+"/test", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&testResult)
	if testResult.Ok {
		t.Fatal("test with a wrong password reported ok=true")
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID+"/databases/"+created.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/databases/"+created.ID, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
}

func TestDatabaseConnectionHTTP_ValidationErrors(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	cases := []databaseConnectionRequest{
		{Name: "", Engine: "postgresql", Host: "h", Database: "d"},
		{Name: "x", Engine: "oracle", Host: "h", Database: "d"},
		{Name: "x", Engine: "postgresql", Host: "", Database: "d"},
		{Name: "x", Engine: "postgresql", Host: "h", Database: ""},
	}
	for _, c := range cases {
		body, _ := json.Marshal(c)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("case %+v: status = %d, want 400, body = %s", c, rec.Code, rec.Body.String())
		}
	}
}

func strPtr(s string) *string { return &s }
