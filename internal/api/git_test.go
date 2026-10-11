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

// gitStatusResponse is the git/status handler's response shape:
// {"files": [...], "branch": {...}}.
type gitStatusResponse struct {
	Files  []protocol.GitFileStatus  `json:"files"`
	Branch *protocol.GitBranchStatus `json:"branch"`
}

func TestGitHTTP_StatusReflectsWorkingTree(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body = %s", rec.Code, rec.Body.String())
	}
	var status gitStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(status.Files) != 0 {
		t.Fatalf("empty repo status = %+v, want none", status.Files)
	}
	if status.Branch == nil || status.Branch.Name != "master" && status.Branch.Name != "main" {
		t.Fatalf("branch = %+v, want current branch name", status.Branch)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(status.Files) != 1 || status.Files[0].Path != "a.txt" || status.Files[0].Index != "?" || status.Files[0].Worktree != "?" {
		t.Fatalf("status after untracked add = %+v", status.Files)
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

	var status gitStatusResponse
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	json.NewDecoder(rec.Body).Decode(&status)
	if len(status.Files) != 1 || status.Files[0].Index != "A" {
		t.Fatalf("status after stage = %+v, want staged A entry", status.Files)
	}

	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/unstage", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unstage: %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	json.NewDecoder(rec.Body).Decode(&status)
	if len(status.Files) != 1 || status.Files[0].Index != "?" {
		t.Fatalf("status after unstage = %+v, want untracked entry", status.Files)
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

// gitTestSetupWithRemote is gitTestSetup with an "origin" bare remote and
// one commit already pushed, for pull/push HTTP tests.
func gitTestSetupWithRemote(t *testing.T) (router http.Handler, adminCookie *http.Cookie, projectID, dir, remoteDir string) {
	t.Helper()
	router, adminCookie, projectID, dir, _ = gitTestSetup(t)
	remoteDir = t.TempDir()
	runGitSetup(t, remoteDir, "init", "-q", "--bare")
	runGitSetup(t, dir, "remote", "add", "origin", remoteDir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "a.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "initial")
	runGitSetup(t, dir, "push", "-q", "-u", "origin", "HEAD")
	return router, adminCookie, projectID, dir, remoteDir
}

func TestGitHTTP_PushNewUpstreamThenExisting(t *testing.T) {
	router, cookie, projectID, dir, remoteDir := gitTestSetupWithRemote(t)
	runGitSetup(t, dir, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "b.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "feature work")

	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/push", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("push: %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body["pushed_new_upstream"] {
		t.Error("pushed_new_upstream = false, want true for a first push")
	}

	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "c.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "more work")

	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/push", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second push: %d, body = %s", rec.Code, rec.Body.String())
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["pushed_new_upstream"] {
		t.Error("pushed_new_upstream = true, want false for an existing upstream")
	}

	remoteLog := runGitSetupOutput(t, remoteDir, "log", "-1", "--pretty=%s", "feature")
	if strings.TrimSpace(remoteLog) != "more work" {
		t.Errorf("remote feature HEAD subject = %q", remoteLog)
	}
}

func TestGitHTTP_PullCleanAndConflict(t *testing.T) {
	router, cookie, projectID, dir, remoteDir := gitTestSetupWithRemote(t)

	other := t.TempDir()
	runGitSetup(t, other, "clone", "-q", remoteDir, ".")
	runGitSetup(t, other, "config", "user.name", "Other User")
	runGitSetup(t, other, "config", "user.email", "other@example.com")
	if err := os.WriteFile(filepath.Join(other, "d.txt"), []byte("from other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, other, "add", "d.txt")
	runGitSetup(t, other, "commit", "-q", "-m", "from other clone")
	runGitSetup(t, other, "push", "-q")

	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/pull", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull: %d, body = %s", rec.Code, rec.Body.String())
	}
	var pullBody struct {
		Conflicts []protocol.GitFileStatus `json:"conflicts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&pullBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pullBody.Conflicts) != 0 {
		t.Fatalf("clean pull conflicts = %+v, want none", pullBody.Conflicts)
	}
	if _, err := os.Stat(filepath.Join(dir, "d.txt")); err != nil {
		t.Errorf("d.txt not pulled in: %v", err)
	}

	if err := os.WriteFile(filepath.Join(other, "a.txt"), []byte("from other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, other, "commit", "-qam", "conflicting change from other")
	runGitSetup(t, other, "push", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("from local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "commit", "-qam", "conflicting local change")

	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/pull", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("conflicting pull: %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&pullBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pullBody.Conflicts) != 1 || pullBody.Conflicts[0].Path != "a.txt" {
		t.Fatalf("conflicts = %+v, want one entry for a.txt", pullBody.Conflicts)
	}
}

func runGitSetupOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestGitHTTP_OwnershipEnforced(t *testing.T) {
	router, _, projectID, _, db := gitTestSetup(t)

	if _, err := auth.CreateUser(context.Background(), db.Pool, "bob-git@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bob := loginAs(t, router, "bob-git@example.com", "password123")

	rec := gitReq(t, router, bob, http.MethodGet, gitURL(projectID, "/status", ""), nil)
	// Project access is server-wide (ADR-037): bob may use another user's
	// project.
	if rec.Code != http.StatusOK {
		t.Errorf("cross-user status: status = %d, want 200", rec.Code)
	}
}

func TestGitHTTP_Discard(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "a.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Untracked path refused with 502 (runtime error text passed through).
	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/discard", ""), []byte(`{"path":"b.txt"}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("discard untracked: %d (%s), want 502 refusal", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "untracked") {
		t.Errorf("refusal = %s", rec.Body.String())
	}
	// Clean path refused.
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/discard", ""), []byte(`{"path":"nonexistent.txt"}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("discard clean: %d (%s), want 502 refusal", rec.Code, rec.Body.String())
	}
	// Traversal refused with 400.
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/discard", ""), []byte(`{"path":"../escape.txt"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discard traversal: %d, want 400", rec.Code)
	}
	// Tracked-modified path restored.
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/discard", ""), []byte(`{"path":"a.txt"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("discard: %d (%s), want 204", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\n" {
		t.Errorf("a.txt content = %q, want restored", content)
	}
}

func TestGitHTTP_Amend(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "a.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "initial")
	before := runGitSetupOutput(t, dir, "rev-parse", "HEAD")

	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/amend", ""), []byte(`{"message":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty amend message: %d, want 400", rec.Code)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/amend", ""), []byte(`{"message":"amended subject"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("amend: %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body["commit"]) != 40 || body["commit"] == strings.TrimSpace(before) {
		t.Errorf("amend commit = %q", body["commit"])
	}
	if subject := strings.TrimSpace(runGitSetupOutput(t, dir, "log", "-1", "--pretty=%s")); subject != "amended subject" {
		t.Errorf("amended subject = %q", subject)
	}
}

func TestGitHTTP_AmendAfterPushRefused(t *testing.T) {
	router, cookie, projectID, _, _ := gitTestSetupWithRemote(t)
	rec := gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/amend", ""), []byte(`{"message":"rewrite"}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("amend after push: %d (%s), want 502 refusal", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already on the remote") {
		t.Errorf("refusal = %s", rec.Body.String())
	}
}

func TestGitHTTP_BranchesCheckoutBranch(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetupWithRemote(t)

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/branches", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("branches: %d (%s)", rec.Code, rec.Body.String())
	}
	var branches []protocol.GitBranchInfo
	if err := json.NewDecoder(rec.Body).Decode(&branches); err != nil {
		t.Fatal(err)
	}
	if len(branches) == 0 {
		t.Fatal("branches = [], want at least the current branch")
	}
	var found bool
	for _, b := range branches {
		if b.Name == "master" {
			found = true
			if !b.Current {
				t.Errorf("master = %+v, want current", b)
			}
		}
	}
	if !found {
		t.Errorf("branches = %+v, want master", branches)
	}

	// Empty / dash-prefixed names are 400 client-side.
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/branch", ""), []byte(`{"name":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty branch name: %d, want 400", rec.Code)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/checkout", ""), []byte(`{"name":"-b main"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dash branch name: %d, want 400", rec.Code)
	}

	// Create-and-switch, then switch back.
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/branch", ""), []byte(`{"name":"topic"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("branch: %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if head := strings.TrimSpace(runGitSetupOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); head != "topic" {
		t.Errorf("HEAD after branch = %q", head)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/checkout", ""), []byte(`{"name":"master"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("checkout: %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if head := strings.TrimSpace(runGitSetupOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); head != "master" {
		t.Errorf("HEAD after checkout = %q", head)
	}

	// Checkout is refused on a dirty tree.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = gitReq(t, router, cookie, http.MethodPost, gitURL(projectID, "/checkout", ""), []byte(`{"name":"topic"}`))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "working tree is dirty") {
		t.Fatalf("checkout dirty: %d (%s), want 502 dirty refusal", rec.Code, rec.Body.String())
	}
}
