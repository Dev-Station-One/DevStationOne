package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/protocol"
)

// gitTestSetup is filesTestSetup with the Project directory initialized as a
// real git repo (local user.name/user.email, so commits work regardless of
// the test environment's global git config).
func gitTestSetup(t *testing.T) (router http.Handler, adminCookie *http.Cookie, projectID, dir string, db *database.DB) {
	t.Helper()
	router, adminCookie, projectID, dir, db = filesTestSetup(t)
	runGitSetup(t, dir, "init", "-q")
	runGitSetup(t, dir, "config", "user.name", "Test User")
	runGitSetup(t, dir, "config", "user.email", "test@example.com")
	return router, adminCookie, projectID, dir, db
}

func runGitSetup(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitReq(t *testing.T, router http.Handler, cookie *http.Cookie, method, path string, body []byte) *httptest.ResponseRecorder {
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

func gitURL(projectID, suffix, query string) string {
	u := "/api/v1/projects/" + projectID + "/git" + suffix
	if query != "" {
		u += "?" + query
	}
	return u
}

func TestGitHTTP_StatusReflectsWorkingTree(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body = %s", rec.Code, rec.Body.String())
	}
	var files []protocol.GitFileStatus
	if err := json.NewDecoder(rec.Body).Decode(&files); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("empty repo status = %+v, want none", files)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	if err := json.NewDecoder(rec.Body).Decode(&files); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(files) != 1 || files[0].Path != "a.txt" || files[0].Index != "?" || files[0].Worktree != "?" {
		t.Fatalf("status after untracked add = %+v", files)
	}
}

func TestGitHTTP_Diff(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "a.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/diff", "path=a.txt"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body["diff"], "+hello world") {
		t.Errorf("diff = %q, want a unified diff", body["diff"])
	}
}

func TestGitHTTP_StageUnstageCommit(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/stage", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stage: %d, body = %s", rec.Code, rec.Body.String())
	}

	var files []protocol.GitFileStatus
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	json.NewDecoder(rec.Body).Decode(&files)
	if len(files) != 1 || files[0].Index != "A" {
		t.Fatalf("status after stage = %+v, want staged A entry", files)
	}

	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/unstage", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unstage: %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	json.NewDecoder(rec.Body).Decode(&files)
	if len(files) != 1 || files[0].Index != "?" {
		t.Fatalf("status after unstage = %+v, want untracked entry", files)
	}

	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/stage", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-stage: %d", rec.Code)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/commit", ""), []byte(`{"message":"add a.txt"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: %d, body = %s", rec.Code, rec.Body.String())
	}
	var commitBody map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&commitBody); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if len(commitBody["commit"]) != 40 {
		t.Errorf("commit sha = %q", commitBody["commit"])
	}

	cmd := exec.Command("git", "log", "-1", "--pretty=%s")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "add a.txt" {
		t.Errorf("commit message = %q", out)
	}
}

func TestGitHTTP_CommitEmptyMessageRejected(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/stage", ""), []byte(`{"path":"a.txt"}`))

	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/commit", ""), []byte(`{"message":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty commit message: status = %d, want 400", rec.Code)
	}
}

func TestGitHTTP_StageMissingPathRejected(t *testing.T) {
	router, cookie, projectID, _, _ := gitTestSetup(t)
	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/stage", ""), []byte(`{"path":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing path: status = %d, want 400", rec.Code)
	}
}

func TestGitHTTP_TraversalRejected(t *testing.T) {
	router, cookie, projectID, _, _ := gitTestSetup(t)

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/diff", "path=../escape.txt"), nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("diff traversal: status = %d, want 400", rec.Code)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/stage", ""), []byte(`{"path":"../escape.txt"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("stage traversal: status = %d, want 400", rec.Code)
	}
}

func TestGitHTTP_OwnershipEnforced(t *testing.T) {
	router, _, projectID, _, db := gitTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob-git@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bob := loginAs(t, router, "bob-git@example.com", "password123")

	rec := gitReq(t, router, bob, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-user status: status = %d, want 404", rec.Code)
	}
}
