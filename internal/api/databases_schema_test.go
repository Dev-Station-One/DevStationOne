package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"devstationone/internal/protocol"
	"devstationone/internal/testdb"
)

func createTestConnection(t *testing.T, router http.Handler, cookie *http.Cookie, projectID string, req databaseConnectionRequest) databaseConnectionResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(body))
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var c databaseConnectionResponse
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return c
}

func getJSON(t *testing.T, router http.Handler, cookie *http.Cookie, url string, out any) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, url, nil)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v (body = %s)", url, err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestDatabaseSchemaHTTP_Postgres(t *testing.T) {
	router, _, cookie, projectID, db := sessionsTestSetup(t)

	if _, err := db.Pool.Exec(context.Background(), `DROP TABLE IF EXISTS api_schema_test`); err != nil {
		t.Fatalf("drop fixture: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `CREATE TABLE api_schema_test (id serial PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	t.Cleanup(func() { db.Pool.Exec(context.Background(), `DROP TABLE IF EXISTS api_schema_test`) })

	c := createTestConnection(t, router, cookie, projectID, databaseConnectionRequest{
		Name: "primary", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase, SSLMode: "disable",
	})

	var schemas []string
	if code := getJSON(t, router, cookie, "/api/v1/projects/"+projectID+"/databases/"+c.ID+"/schemas", &schemas); code != http.StatusOK {
		t.Fatalf("schemas: status = %d", code)
	}
	found := false
	for _, s := range schemas {
		if s == "public" {
			found = true
		}
	}
	if !found {
		t.Fatalf("schemas = %v, want it to include public", schemas)
	}

	var tables []protocol.DBTableInfo
	if code := getJSON(t, router, cookie, "/api/v1/projects/"+projectID+"/databases/"+c.ID+"/tables?schema=public", &tables); code != http.StatusOK {
		t.Fatalf("tables: status = %d", code)
	}
	foundTable := false
	for _, tb := range tables {
		if tb.Name == "api_schema_test" && tb.Kind == "table" {
			foundTable = true
		}
	}
	if !foundTable {
		t.Fatalf("tables = %v, want it to include api_schema_test", tables)
	}

	var cols []protocol.DBColumnInfo
	url := "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/tables/api_schema_test/columns?schema=public"
	if code := getJSON(t, router, cookie, url, &cols); code != http.StatusOK {
		t.Fatalf("columns: status = %d", code)
	}
	if len(cols) != 2 || cols[0].Name != "id" || cols[1].Name != "name" {
		t.Fatalf("columns = %+v, want [id, name]", cols)
	}

	var idx []protocol.DBIndexInfo
	url = "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/tables/api_schema_test/indexes?schema=public"
	if code := getJSON(t, router, cookie, url, &idx); code != http.StatusOK || len(idx) == 0 {
		t.Fatalf("indexes: status = %d, idx = %+v, want a primary key index", code, idx)
	}

	var cons []protocol.DBConstraintInfo
	url = "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/tables/api_schema_test/constraints?schema=public"
	if code := getJSON(t, router, cookie, url, &cons); code != http.StatusOK {
		t.Fatalf("constraints: status = %d", code)
	}
	if len(cons) != 1 || cons[0].Type != "primary_key" {
		t.Fatalf("constraints = %+v, want one primary_key", cons)
	}

	var trig []protocol.DBTriggerInfo
	url = "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/tables/api_schema_test/triggers?schema=public"
	if code := getJSON(t, router, cookie, url, &trig); code != http.StatusOK {
		t.Fatalf("triggers: status = %d", code)
	}
	if len(trig) != 0 {
		t.Fatalf("triggers = %+v, want none", trig)
	}
}

// TestDatabaseConnectionHTTP_EditInvalidatesCachedPool proves that editing a
// connection's stored secret actually reaches the runtime's connection pool
// cache (ADR-021/022) — without the invalidate RPC, the runtime would keep
// answering "test" successfully off the old, already-open pool even after
// the credentials on file are wrong.
func TestDatabaseConnectionHTTP_EditInvalidatesCachedPool(t *testing.T) {
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	c := createTestConnection(t, router, cookie, projectID, databaseConnectionRequest{
		Name: "primary", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase, SSLMode: "disable",
	})

	// Warm the runtime's cached pool with a passing test.
	var result struct {
		Ok bool `json:"ok"`
	}
	if code := postJSON(t, router, cookie, "/api/v1/projects/"+projectID+"/databases/"+c.ID+"/test", nil, &result); code != http.StatusOK || !result.Ok {
		t.Fatalf("initial test: status = %d, ok = %v", code, result.Ok)
	}

	// Break the credentials via PATCH — must invalidate the cached pool.
	patchBody, _ := json.Marshal(databaseConnectionPatchRequest{
		Secret: &struct {
			Password    string `json:"password"`
			ExtraParams string `json:"extra_params"`
		}{Password: "definitely-wrong"},
	})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID+"/databases/"+c.ID, bytes.NewReader(patchBody))
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch secret: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if code := postJSON(t, router, cookie, "/api/v1/projects/"+projectID+"/databases/"+c.ID+"/test", nil, &result); code != http.StatusOK {
		t.Fatalf("test after breaking password: status = %d", code)
	}
	if result.Ok {
		t.Fatal("test after breaking the password reported ok=true — the runtime's cached pool was not invalidated")
	}
}

func postJSON(t *testing.T, router http.Handler, cookie *http.Cookie, url string, body []byte, out any) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v (body = %s)", url, err, rec.Body.String())
	}
	return rec.Code
}
