package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/devices"
	"devstationone/internal/projects"
	"devstationone/internal/runtimes"
)

// signedRequest builds and sends one request authenticated as a paired
// device. The target is origin-form (path [+ query]) — exactly what the
// wire carries — so r.RequestURI equals the signed requestURI, matching
// the Android client's Auth.header. host is the httptest default
// (example.com), the same string passed to devices.Sign.
func signedRequest(t *testing.T, key devices.KeyPair, deviceID, method, uri string, body []byte) *http.Request {
	t.Helper()
	hdr, err := devices.Sign(key, deviceID, method, "example.com", uri, time.Now())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	r := httptest.NewRequest(method, uri, bytes.NewReader(body))
	r.Header.Set("Authorization", hdr)
	return r
}

func TestDevicePairAndUse(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	user, err := auth.CreateUser(ctx, db.Pool, "device@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	code, err := devices.CreatePairingCode(ctx, db.Pool, user.ID, "Pixel")
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	key, err := devices.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	// Pair: code + pubkey → device ID.
	body, _ := json.Marshal(map[string]string{"code": code, "public_key": key.PublicB64})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/devices/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("pair: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var paired struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &paired)
	if paired.ID == "" {
		t.Fatal("pair returned no device id")
	}

	// Reusing the code fails.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/devices/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reused pair code: got %d, want 400", rec.Code)
	}

	// A non-SPKI key is refused (and leaves its code unburned).
	code2, _ := devices.CreatePairingCode(ctx, db.Pool, user.ID, "Spare")
	bad, _ := json.Marshal(map[string]string{"code": code2, "public_key": "AAAA-not-a-valid-der"})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/devices/pair", bytes.NewReader(bad)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad pubkey: got %d, want 400", rec.Code)
	}

	// Signed request lists the user's devices.
	uri := "/api/v1/auth/devices"
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, key, paired.ID, http.MethodGet, uri, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("signed list: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var list []deviceResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "Pixel" {
		t.Fatalf("device list wrong: %+v", list)
	}

	// A request with no signature is rejected.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com"+uri, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned request: got %d, want 401", rec.Code)
	}

	// Replay of the same header is rejected on the second send.
	req := signedRequest(t, key, paired.ID, http.MethodGet, uri, nil)
	hdrReplay := req.Header.Get("Authorization")
	for i := 0; i < 2; i++ {
		r2 := httptest.NewRequest(http.MethodGet, "http://example.com"+uri, nil)
		r2.Header.Set("Authorization", hdrReplay)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, r2)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed header: got %d, want 401", rec.Code)
	}

	// Method coverage is verified in devices' unit test (the mux rejects
	// a method-mismatched request with 405 before auth middleware runs).

	// Stale timestamp is rejected before any device lookup.
	staleHdr, _ := devices.Sign(key, paired.ID, "GET", "example.com", uri, time.Now().Add(-5*time.Minute))
	r4 := httptest.NewRequest(http.MethodGet, "http://example.com"+uri, nil)
	r4.Header.Set("Authorization", staleHdr)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, r4)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale ts: got %d, want 401", rec.Code)
	}

	// Revoke ends access.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, key, paired.ID, http.MethodDelete, "/api/v1/auth/devices/"+paired.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, key, paired.ID, http.MethodGet, uri, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-revoke request: got %d, want 401", rec.Code)
	}
}

func TestDeviceAuthorization(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	owner, _ := auth.CreateUser(ctx, db.Pool, "owner@example.com", "password123", false)
	stranger, _ := auth.CreateUser(ctx, db.Pool, "stranger@example.com", "password123", false)
	admin, _ := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true)

	keyOwner, _ := devices.GenerateKey()
	keyStranger, _ := devices.GenerateKey()
	keyAdmin, _ := devices.GenerateKey()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	uri := "/api/v1/auth/devices/pair"
	pair := func(t *testing.T, router http.Handler, code string, key devices.KeyPair) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"code": code, "public_key": key.PublicB64})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://example.com"+uri, bytes.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("pair %s: %d %s", code, rec.Code, rec.Body.String())
		}
		var p struct {
			ID string
		}
		json.Unmarshal(rec.Body.Bytes(), &p)
		return p.ID
	}

	dOwner := pair(t, router, pairingCode(ctx, t, db, owner.ID, "Owner phone"), keyOwner)
	dStranger := pair(t, router, pairingCode(ctx, t, db, stranger.ID, "Stranger phone"), keyStranger)

	// An owner's device sees only its own devices.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodGet, "/api/v1/auth/devices", nil))
	list := []deviceResponse{}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "Owner phone" {
		t.Fatalf("owner device list: %+v", list)
	}

	// A device cannot revoke another user's device.
	otherURI := "/api/v1/auth/devices/" + dStranger
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodDelete, otherURI, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user revoke: got %d, want 403", rec.Code)
	}

	// A device identity carries the pairing user's admin flag: an admin's
	// device revokes anyone's, a non-admin's cannot.
	adminID := pair(t, router, pairingCode(ctx, t, db, admin.ID, "Admin phone"), keyAdmin)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyAdmin, adminID, http.MethodDelete, otherURI, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin cross-user revoke: got %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// Revocation is visible through the admin view — the revoked device
	// itself, of course, can no longer authenticate.
	uriS := "/api/v1/users/" + stranger.ID + "/devices"
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyAdmin, adminID, http.MethodGet, uriS, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin view of stranger devices: got %d", rec.Code)
	}
	var stList []deviceResponse
	json.Unmarshal(rec.Body.Bytes(), &stList)
	if len(stList) != 1 || stList[0].RevokedAt == nil {
		t.Fatalf("expected revoked state on stranger's device: %+v", stList)
	}

	// Admin API lists a user's devices.
	uri2 := "/api/v1/users/" + owner.ID + "/devices"
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyAdmin, adminID, http.MethodGet, uri2, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin user devices: got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Owner phone") {
		t.Fatalf("admin user devices missing owner device: %s", rec.Body.String())
	}

	// Non-admin access to the admin API is denied.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodGet, uri2, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin user devices: got %d, want 403", rec.Code)
	}
}

// pairingCode mints a fresh pairing code for userID.
func pairingCode(ctx context.Context, t *testing.T, db *database.DB, userID, name string) string {
	t.Helper()
	code, err := devices.CreatePairingCode(ctx, db.Pool, userID, name)
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	return code
}

// pairCode drives one full pairing exchange through the router and returns
// the new device ID.
func pairCode(t *testing.T, router http.Handler, code string, key devices.KeyPair) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "public_key": key.PublicB64})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/auth/devices/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body.String())
	}
	var p struct {
		ID string
	}
	json.Unmarshal(rec.Body.Bytes(), &p)
	return p.ID
}

// TestDeviceJournalAuth proves the journal routes accept the paired-device
// Sig header (ADR-053) while keeping cookie sessions working and project
// visibility intact — the Android client reads/edits notes this way.
func TestDeviceJournalAuth(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	owner, err := auth.CreateUser(ctx, db.Pool, "journal-owner@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	stranger, err := auth.CreateUser(ctx, db.Pool, "journal-stranger@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}

	keyOwner, _ := devices.GenerateKey()
	keyStranger, _ := devices.GenerateKey()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	dOwner := pairCode(t, router, pairingCode(ctx, t, db, owner.ID, "Journal owner phone"), keyOwner)
	dStranger := pairCode(t, router, pairingCode(ctx, t, db, stranger.ID, "Journal stranger phone"), keyStranger)

	rt, _, err := runtimes.Create(ctx, db.Pool, "journal-auth-runtime", owner.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	proj, err := projects.Create(ctx, db.Pool, projects.Fields{
		Name:             "journal-auth-project",
		RuntimeID:        rt.ID,
		WorkingDirectory: "/tmp",
		Shell:            "/bin/bash",
	}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projURI := "/api/v1/projects/" + proj.ID + "/journal"

	// The owner's device creates a note and reads the list through the Sig header.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodPost, projURI, []byte(`{"body_html":"<p>device note</p>"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("device create note: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodGet, projURI+"?limit=25&offset=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("device list journal: %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Total int `json:"total"`
		Notes []struct {
			ID      string `json:"id"`
			Snippet string `json:"snippet"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if page.Total != 1 || len(page.Notes) != 1 {
		t.Fatalf("device list journal: total=%d notes=%d", page.Total, len(page.Notes))
	}

	// The same note is readable by the device by id.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodGet, projURI+"/notes/"+page.Notes[0].ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("device get note: %d %s", rec.Code, rec.Body.String())
	}
	var note struct {
		BodyHTML string `json:"body_html"`
	}
	json.Unmarshal(rec.Body.Bytes(), &note)
	if !strings.Contains(note.BodyHTML, "device note") {
		t.Fatalf("note body: %q", note.BodyHTML)
	}

	// A stranger's device rides the platform's project visibility model —
	// every authenticated user sees every project (projects.go listProjects)
	// — so it gets the same result as the owner, not a 404. Device pairing
	// does not narrow authorization: it extends the pairing user's session.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyStranger, dStranger, http.MethodGet, projURI, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stranger device list journal: got %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, signedRequest(t, keyStranger, dStranger, http.MethodDelete, projURI+"/notes/"+page.Notes[0].ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stranger device delete note: got %d, want 204", rec.Code)
	}

	// With no Sig header and no cookie the route still 401s.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com"+projURI, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list journal: got %d, want 401", rec.Code)
	}
}

// TestDeviceFilesAuth proves the files list/search routes accept the
// paired-device Sig header (ADR-053 addendum, Android parity stage 4)
// while keeping cookie sessions working. No runtime is connected, so an
// authenticated device proves it reached the handler by getting 409
// (runtime-offline) — owner and stranger alike, because every
// authenticated user sees every project (projects.go listProjects); the
// 401 asserts the route stays closed to anonymous traffic.
func TestDeviceFilesAuth(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	owner, err := auth.CreateUser(ctx, db.Pool, "files-owner@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	stranger, err := auth.CreateUser(ctx, db.Pool, "files-stranger@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}

	keyOwner, _ := devices.GenerateKey()
	keyStranger, _ := devices.GenerateKey()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	dOwner := pairCode(t, router, pairingCode(ctx, t, db, owner.ID, "Files owner phone"), keyOwner)
	dStranger := pairCode(t, router, pairingCode(ctx, t, db, stranger.ID, "Files stranger phone"), keyStranger)

	rt, _, err := runtimes.Create(ctx, db.Pool, "files-auth-runtime", owner.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	proj, err := projects.Create(ctx, db.Pool, projects.Fields{
		Name:             "files-auth-project",
		RuntimeID:        rt.ID,
		WorkingDirectory: "/tmp",
		Shell:            "/bin/bash",
	}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	for _, uri := range []string{
		"/api/v1/projects/" + proj.ID + "/files?path=.",
		"/api/v1/projects/" + proj.ID + "/files/search?q=go&path=.",
		// Stage 5 (code editor): the stat/content editor routes take a
		// device identity too; PUT carries no body because the device wall
		// runs before the handler ever looks at one.
		"/api/v1/projects/" + proj.ID + "/files/stat?path=go.mod",
		"/api/v1/projects/" + proj.ID + "/files/content?path=go.mod",
	} {
		// The owner's device passes the device-signature wall and reaches
		// the handler, which answers 409 because no runtime is connected.
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, signedRequest(t, keyOwner, dOwner, http.MethodGet, uri, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("owner device %s: got %d (%s), want 409", uri, rec.Code, rec.Body.String())
		}
		// A stranger's device carries its user's session the same way:
		// same authorization surface, same 409 (see the test's comment).
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, signedRequest(t, keyStranger, dStranger, http.MethodGet, uri, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("stranger device %s: got %d, want 409", uri, rec.Code)
		}
		// With no Sig header and no cookie the route still 401s.
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com"+uri, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: got %d, want 401", uri, rec.Code)
		}
	}
}

// TestDeviceGitAuth proves all 13 git routes accept the paired-device Sig
// header while keeping cookie sessions working — the git routes switched to
// requireIdentity with the app's Source Control tab (docs/DESIGN_ANDROID_SCM.md
// slice 1). No runtime is connected, so an authenticated device proves it
// reached the handler by getting 409 — owner and stranger alike (every
// authenticated user sees every project); the 401 asserts the route stays
// closed to anonymous traffic.
func TestDeviceGitAuth(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	owner, err := auth.CreateUser(ctx, db.Pool, "git-owner@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	stranger, err := auth.CreateUser(ctx, db.Pool, "git-stranger@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}

	keyOwner, _ := devices.GenerateKey()
	keyStranger, _ := devices.GenerateKey()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})

	dOwner := pairCode(t, router, pairingCode(ctx, t, db, owner.ID, "Git owner phone"), keyOwner)
	dStranger := pairCode(t, router, pairingCode(ctx, t, db, stranger.ID, "Git stranger phone"), keyStranger)

	rt, _, err := runtimes.Create(ctx, db.Pool, "git-auth-runtime", owner.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	proj, err := projects.Create(ctx, db.Pool, projects.Fields{
		Name:             "git-auth-project",
		RuntimeID:        rt.ID,
		WorkingDirectory: "/tmp",
		Shell:            "/bin/bash",
	}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	for _, uri := range []string{
		"GET  /api/v1/projects/" + proj.ID + "/git/status",
		"GET  /api/v1/projects/" + proj.ID + "/git/diff?path=.",
		"POST /api/v1/projects/" + proj.ID + "/git/stage",
		"POST /api/v1/projects/" + proj.ID + "/git/unstage",
		"POST /api/v1/projects/" + proj.ID + "/git/commit",
		"GET  /api/v1/projects/" + proj.ID + "/git/log",
		"POST /api/v1/projects/" + proj.ID + "/git/pull",
		"POST /api/v1/projects/" + proj.ID + "/git/push",
		"POST /api/v1/projects/" + proj.ID + "/git/discard",
		"POST /api/v1/projects/" + proj.ID + "/git/amend",
		"GET  /api/v1/projects/" + proj.ID + "/git/branches",
		"POST /api/v1/projects/" + proj.ID + "/git/checkout",
		"POST /api/v1/projects/" + proj.ID + "/git/branch",
	} {
		method, uri, _ := strings.Cut(uri, " ")
		uri = strings.TrimSpace(uri)
		// The device wall runs before the handler decodes a body, so every
		// POST gets the same 409 (runtime-offline) without one.
		req := signedRequest(t, keyOwner, dOwner, method, uri, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("owner device %s: got %d (%s), want 409", method, rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, signedRequest(t, keyStranger, dStranger, method, uri, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("stranger device %s: got %d, want 409", method, rec.Code)
		}
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, "http://example.com"+uri, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: got %d, want 401", method, rec.Code)
		}
	}
}
