package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devstationone/internal/auth"
	"devstationone/internal/database"
)

// filesTestSetup is sessionsTestSetup with the Project rooted at a dedicated
// temp directory, so file-manager assertions see only their own entries.
func filesTestSetup(t *testing.T) (router http.Handler, adminCookie *http.Cookie, projectID, projectDir string, db *database.DB) {
	t.Helper()
	projectDir = t.TempDir()
	router, _, adminCookie, projectID, db = sessionsTestSetupIn(t, projectDir)
	return router, adminCookie, projectID, projectDir, db
}

func fileReq(t *testing.T, router http.Handler, cookie *http.Cookie, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func fileURL(projectID, suffix, query string) string {
	u := "/api/v1/projects/" + projectID + "/files" + suffix
	if query != "" {
		u += "?" + query
	}
	return u
}

func TestFileHTTP_ListStatCreateMkdir(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "", "path=."), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var entries []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list returned %d entries, want 2", len(entries))
	}
	byName := map[string]map[string]any{}
	for _, e := range entries {
		byName[e["name"].(string)] = e
	}
	if byName["hello.txt"]["type"] != "file" || byName["hello.txt"]["size"].(float64) != 11 {
		t.Errorf("hello.txt entry = %v", byName["hello.txt"])
	}
	if byName["sub"]["type"] != "dir" {
		t.Errorf("sub entry = %v", byName["sub"])
	}

	rec = fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/stat", "path=hello.txt"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stat: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/create", ""), []byte(`{"path":"created.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	if err != nil || len(data) != 0 {
		t.Errorf("created.txt = %q, err %v", data, err)
	}

	rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/mkdir", ""), []byte(`{"path":"newdir"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mkdir: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if st, err := os.Stat(filepath.Join(dir, "newdir")); err != nil || !st.IsDir() {
		t.Errorf("newdir stat = %v, err %v", st, err)
	}
}

func TestFileHTTP_WriteReadDownload(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)

	rec := fileReq(t, router, cookie, http.MethodPut, fileURL(projectID, "/content", "path=uploaded.txt"), []byte("uploaded bytes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "uploaded.txt"))
	if err != nil || string(data) != "uploaded bytes" {
		t.Errorf("uploaded.txt on disk = %q, err %v", data, err)
	}

	rec = fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/content", "path=uploaded.txt"), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "uploaded bytes" {
		t.Errorf("read: status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	rec = fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/content", "path=uploaded.txt&download=1"), nil)
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="uploaded.txt"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestFileHTTP_HTMLForceAttachment(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("<script>alert(1)</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/content", "path=page.html"), nil)
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("html content must force attachment, got %q", cd)
	}
}

func TestFileHTTP_RenameCopyMoveDelete(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "orig.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/copy", ""), []byte(`{"from":"orig.txt","to":"copied.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("copy: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/rename", ""), []byte(`{"from":"orig.txt","to":"renamed.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("rename: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/move", ""), []byte(`{"from":"renamed.txt","to":"moved/renamed.txt"}`))
	if rec.Code == http.StatusNoContent {
		t.Fatal("move into a missing directory should fail")
	}
	if err := os.Mkdir(filepath.Join(dir, "moved"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/move", ""), []byte(`{"from":"renamed.txt","to":"moved/renamed.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("move: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fileReq(t, router, cookie, http.MethodDelete, fileURL(projectID, "", "path=copied.txt"), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "copied.txt")); !os.IsNotExist(err) {
		t.Errorf("copied.txt should be gone, err = %v", err)
	}
}

func TestFileHTTP_Chmod(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/mode", ""), []byte(`{"path":"a.txt","mode":"640"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("chmod: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("perm = %o, want 640", info.Mode().Perm())
	}

	rec = fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/mode", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chmod without mode: status = %d, want 400", rec.Code)
	}
}

func TestFileHTTP_ChmodRecursive(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/mode", ""), []byte(`{"path":"d","mode":"700","recursive":true}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("chmod recursive: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(dir, "d", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("child perm = %o, want 700", info.Mode().Perm())
	}
}

func TestFileHTTP_ChownToSelf(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skip("no current user available")
	}

	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"path": "a.txt", "owner": u.Username})
	rec := fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/owner", ""), body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("chown to self: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/owner", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chown without owner/group: status = %d, want 400", rec.Code)
	}
}

func TestFileHTTP_ChownUnknownUser(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodPatch, fileURL(projectID, "/owner", ""), []byte(`{"path":"a.txt","owner":"devstationone-nonexistent-user-xyz"}`))
	if rec.Code == http.StatusNoContent {
		t.Fatal("chown to an unknown user should fail")
	}
}

func TestFileHTTP_Search(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.log", "b.txt", "sub/c.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/search", "q=.log&path=."), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var matches []string
	if err := json.NewDecoder(rec.Body).Decode(&matches); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("search matches = %v, want 2", matches)
	}
}

func TestFileHTTP_TraversalRejected(t *testing.T) {
	router, cookie, projectID, _, _ := filesTestSetup(t)

	for _, tc := range []struct{ name, path string }{
		{"absolute", "/etc/passwd"},
		{"parent", "../escape.txt"},
		{"nested parent", "sub/../../escape.txt"},
		{"empty", ""},
	} {
		rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "", "path="+tc.path), nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: list status = %d, want 400", tc.name, rec.Code)
		}
		rec = fileReq(t, router, cookie, http.MethodPost, fileURL(projectID, "/create", ""), []byte(fmt.Sprintf(`{"path":%q}`, tc.path)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: create status = %d, want 400", tc.name, rec.Code)
		}
	}
}

func TestFileHTTP_SymlinkEscapeRejected(t *testing.T) {
	router, cookie, projectID, dir, _ := filesTestSetup(t)

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}

	rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "/content", "path=escape"), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("symlink escape read: status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Error("response must not contain the outside file's content")
	}

	rec = fileReq(t, router, cookie, http.MethodPut, fileURL(projectID, "/content", "path=escape"), []byte("evil"))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("symlink escape write: status = %d, want 502", rec.Code)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "secret" {
		t.Errorf("outside file changed: %q, err %v", data, err)
	}
}

func TestFileHTTP_UploadTooLarge(t *testing.T) {
	router, cookie, projectID, _, _ := filesTestSetup(t)

	big := make([]byte, 24<<20+1)
	rec := fileReq(t, router, cookie, http.MethodPut, fileURL(projectID, "/content", "path=big.bin"), big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload: status = %d, want 413", rec.Code)
	}
}

func TestFileHTTP_OwnershipEnforced(t *testing.T) {
	router, _, projectID, _, db := filesTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bob := loginAs(t, router, "bob@example.com", "password123")

	// Project access is server-wide (ADR-037): bob may list and write in
	// another user's project; only the project definition is owner/admin.
	rec := fileReq(t, router, bob, http.MethodGet, fileURL(projectID, "", "path=."), nil)
	if rec.Code != http.StatusOK {
		t.Errorf("cross-user list: status = %d, want 200", rec.Code)
	}
	rec = fileReq(t, router, bob, http.MethodPut, fileURL(projectID, "/content", "path=x.txt"), []byte("x"))
	if rec.Code != http.StatusOK {
		t.Errorf("cross-user upload: status = %d, want 200", rec.Code)
	}
}

func TestFileHTTP_RuntimeOffline(t *testing.T) {
	router, cookie, projectID, _, _ := filesTestSetup(t)

	rec := fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "", "path=."), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list while online: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Revoke the runtime and wait until it drops off the registry.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
	req.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req)
	var list []runtimeResponse
	json.NewDecoder(rec2.Body).Decode(&list)
	var runtimeID string
	for _, rt := range list {
		if rt.Online {
			runtimeID = rt.ID
		}
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/"+runtimeID+"/revoke", nil)
	req.AddCookie(cookie)
	rec2 = httptest.NewRecorder()
	router.ServeHTTP(rec2, req)

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var list []runtimeResponse
		json.NewDecoder(rec.Body).Decode(&list)
		for _, rt := range list {
			if rt.ID == runtimeID {
				return !rt.Online
			}
		}
		return false
	})

	rec = fileReq(t, router, cookie, http.MethodGet, fileURL(projectID, "", "path=."), nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("list while offline: status = %d, want 409", rec.Code)
	}
}
