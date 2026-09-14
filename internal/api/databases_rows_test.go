package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"devstationone/internal/testdb"
)

func TestDatabaseRowsHTTP_Postgres(t *testing.T) {
	router, _, cookie, projectID, db := sessionsTestSetup(t)

	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `DROP TABLE IF EXISTS api_rows_test`); err != nil {
		t.Fatalf("drop fixture: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE api_rows_test (id serial PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	t.Cleanup(func() { db.Pool.Exec(context.Background(), `DROP TABLE IF EXISTS api_rows_test`) })

	c := createTestConnection(t, router, cookie, projectID, databaseConnectionRequest{
		Name: "primary", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: testdb.PostgresPassword, Database: testdb.PostgresDatabase, SSLMode: "disable",
	})

	rowsURL := "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/tables/api_rows_test/rows?schema=public"
	commitURL := rowsURL

	type rowsBody struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
		HasMore bool     `json:"has_more"`
	}

	// Empty table.
	var got rowsBody
	if code := getJSON(t, router, cookie, rowsURL, &got); code != http.StatusOK {
		t.Fatalf("rows (empty): status = %d", code)
	}
	if len(got.Rows) != 0 {
		t.Fatalf("rows (empty) = %+v, want none", got)
	}

	// Insert two rows via commit.
	commitBody, _ := json.Marshal(map[string]any{
		"edits": []map[string]any{
			{"op": "insert", "values": map[string]any{"name": "alice"}},
			{"op": "insert", "values": map[string]any{"name": "bob"}},
		},
	})
	r := httptest.NewRequest(http.MethodPatch, commitURL, bytes.NewReader(commitBody))
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit insert: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if code := getJSON(t, router, cookie, rowsURL+"&limit=1", &got); code != http.StatusOK {
		t.Fatalf("rows (limit=1): status = %d", code)
	}
	if len(got.Rows) != 1 || !got.HasMore {
		t.Fatalf("rows (limit=1) = %+v, want 1 row and has_more=true", got)
	}

	var nameCol int
	for i, col := range got.Columns {
		if col == "name" {
			nameCol = i
		}
	}
	if got.Columns[nameCol] != "name" {
		t.Fatalf("columns = %v, missing name", got.Columns)
	}

	if code := getJSON(t, router, cookie, rowsURL+"&filter="+urlEsc("name = 'bob'"), &got); code != http.StatusOK {
		t.Fatalf("rows (filter): status = %d", code)
	}
	if len(got.Rows) != 1 || got.Rows[0][nameCol] != "bob" {
		t.Fatalf("rows (filter name=bob) = %+v", got)
	}
	bobID := got.Rows[0][0]

	// Update via commit.
	updateBody, _ := json.Marshal(map[string]any{
		"edits": []map[string]any{
			{"op": "update", "original": map[string]any{"id": bobID}, "values": map[string]any{"name": "robert"}},
		},
	})
	r = httptest.NewRequest(http.MethodPatch, commitURL, bytes.NewReader(updateBody))
	r.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit update: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if code := getJSON(t, router, cookie, rowsURL+"&filter="+urlEsc("name = 'robert'"), &got); code != http.StatusOK || len(got.Rows) != 1 {
		t.Fatalf("rows after update = %+v (status %d)", got, code)
	}

	// Delete via commit.
	deleteBody, _ := json.Marshal(map[string]any{
		"edits": []map[string]any{
			{"op": "delete", "original": map[string]any{"id": bobID}},
		},
	})
	r = httptest.NewRequest(http.MethodPatch, commitURL, bytes.NewReader(deleteBody))
	r.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if code := getJSON(t, router, cookie, rowsURL, &got); code != http.StatusOK || len(got.Rows) != 1 {
		t.Fatalf("rows after delete = %+v (status %d), want 1 remaining", got, code)
	}

	// SQL editor: raw SELECT and raw INSERT.
	queryURL := "/api/v1/projects/" + projectID + "/databases/" + c.ID + "/query"
	queryBody, _ := json.Marshal(map[string]string{"sql": "SELECT id, name FROM api_rows_test"})
	var qres struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
		IsExec  bool     `json:"is_exec"`
	}
	if code := postJSON(t, router, cookie, queryURL, queryBody, &qres); code != http.StatusOK {
		t.Fatalf("query select: status = %d", code)
	}
	if qres.IsExec || len(qres.Rows) != 1 {
		t.Fatalf("query select = %+v, want one row", qres)
	}

	queryBody, _ = json.Marshal(map[string]string{"sql": "INSERT INTO api_rows_test (name) VALUES ('dave')"})
	if code := postJSON(t, router, cookie, queryURL, queryBody, &qres); code != http.StatusOK {
		t.Fatalf("query insert: status = %d", code)
	}
	if !qres.IsExec {
		t.Fatalf("query insert = %+v, want is_exec=true", qres)
	}
	if code := getJSON(t, router, cookie, rowsURL+"&filter="+urlEsc("name = 'dave'"), &got); code != http.StatusOK || len(got.Rows) != 1 {
		t.Fatalf("rows after query insert = %+v (status %d)", got, code)
	}
}

func urlEsc(s string) string {
	return url.QueryEscape(s)
}
