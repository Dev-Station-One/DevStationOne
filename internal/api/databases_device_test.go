package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"devstationone/internal/auth"
	"devstationone/internal/dbconnections"
	"devstationone/internal/devices"
	"devstationone/internal/testdb"
)

// TestDeviceDatabasesAuth proves every per-connection database and backup
// route accepts the paired-device Sig header (ADR-053) and stays closed to
// anonymous traffic — the Android Databases tab calls them
// (docs/DESIGN_ANDROID_DATABASES.md slice 1). The handlers answer with
// assorted statuses (400 bad body, 409 runtime offline, ...) depending on
// the route, so passing the device wall is asserted as "neither 401 nor 403".
func TestDeviceDatabasesAuth(t *testing.T) {
	router, _, _, projectID, db := sessionsTestSetup(t)
	ctx := context.Background()

	user, err := auth.CreateUser(ctx, db.Pool, "db-device@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	key, _ := devices.GenerateKey()
	device := pairCode(t, router, pairingCode(ctx, t, db, user.ID, "DB phone"), key)

	conn, err := dbconnections.Create(ctx, db.Pool, projectID, dbconnections.Fields{
		Name: "dev-pg", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Database: testdb.PostgresDatabase, SSLMode: "disable",
	}, dbconnections.SecretUpdate{Password: testdb.PostgresPassword}, testDBEncryptionKey, user.ID)
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	base := "/api/v1/projects/" + projectID + "/databases"
	c := base + "/" + conn.ID
	for _, route := range []string{
		"GET " + base,
		"POST " + base,
		"GET " + c,
		"PATCH " + c,
		"DELETE " + c + "x", // unknown id: proves auth, never deletes the fixture
		"POST " + c + "/test",
		"GET " + c + "/schemas",
		"GET " + c + "/tables",
		"GET " + c + "/tables/t/columns",
		"GET " + c + "/tables/t/indexes",
		"GET " + c + "/tables/t/constraints",
		"GET " + c + "/tables/t/triggers",
		"GET " + c + "/tables/t/rows",
		"PATCH " + c + "/tables/t/rows",
		"POST " + c + "/query",
		"GET " + c + "/principals",
		"POST " + c + "/principals",
		"DELETE " + c + "/principals/p",
		"PATCH " + c + "/principals/p",
		"GET " + c + "/principals/p/grants",
		"POST " + c + "/principals/p/grants",
		"POST " + c + "/principals/p/grants/revoke",
		"GET " + c + "/backups",
		"POST " + c + "/backups",
		"GET " + c + "/backups/schedule",
		"PUT " + c + "/backups/schedule",
		"DELETE " + c + "/backups/schedule",
		"GET " + c + "/backups/b",
		"DELETE " + c + "/backups/b",
		"GET " + c + "/backups/b/download",
		"POST " + c + "/backups/b/restore",
	} {
		method, uri, _ := strings.Cut(route, " ")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, signedRequest(t, key, device, method, uri, nil))
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("device %s: got %d (%s), want past the auth wall", route, rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, "http://example.com"+uri, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: got %d, want 401", route, rec.Code)
		}
	}
}

// TestDatabaseConnectionHTTP_PatchSecretKeepsExtraParams: the API never
// returns extra_params, so a password change that omits them must not wipe
// what the web UI stored (design decision D4).
func TestDatabaseConnectionHTTP_PatchSecretKeepsExtraParams(t *testing.T) {
	router, _, cookie, projectID, db := sessionsTestSetup(t)

	createBody, _ := json.Marshal(databaseConnectionRequest{
		Name: "extra", Engine: "postgresql", Host: testdb.PostgresHost, Port: testdb.PostgresPort,
		Username: testdb.PostgresUser, Password: "old-pass", Database: testdb.PostgresDatabase,
		SSLMode: "disable", ExtraParams: "application_name=keepme",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/databases", bytes.NewReader(createBody))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created databaseConnectionResponse
	json.NewDecoder(rec.Body).Decode(&created)

	patch := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+projectID+"/databases/"+created.ID, strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	ctx := context.Background()

	patch(`{"secret":{"password":"new-pass"}}`)
	p, err := dbconnections.ResolveParams(ctx, db.Pool, created.ID, testDBEncryptionKey)
	if err != nil || p.Password != "new-pass" || p.ExtraParams != "application_name=keepme" {
		t.Fatalf("omitted extra_params: password=%q extra=%q err=%v, want new-pass / application_name=keepme", p.Password, p.ExtraParams, err)
	}

	patch(`{"secret":{"password":"new-pass","extra_params":""}}`)
	p, err = dbconnections.ResolveParams(ctx, db.Pool, created.ID, testDBEncryptionKey)
	if err != nil || p.ExtraParams != "" {
		t.Fatalf("explicit empty extra_params: extra=%q err=%v, want cleared", p.ExtraParams, err)
	}
}
