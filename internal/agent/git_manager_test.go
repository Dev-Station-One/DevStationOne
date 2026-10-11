package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"devstationone/internal/protocol"
)

// initGitRepo creates a fresh git repo in a temp dir with local
// user.name/user.email set (so commits work regardless of the test
// environment's global git config) and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runSetupGit(t, dir, "init", "-q")
	runSetupGit(t, dir, "config", "user.name", "Test User")
	runSetupGit(t, dir, "config", "user.email", "test@example.com")
	return dir
}

func runSetupGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestExecuteGit_StatusUntrackedModifiedAndClean(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status on clean repo: %v", err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("clean repo status = %+v, want no files", result.Files)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err = executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	byPath := map[string]protocol.GitFileStatus{}
	for _, f := range result.Files {
		byPath[f.Path] = f
	}
	if got := byPath["a.txt"]; got.Index != "." || got.Worktree != "M" {
		t.Errorf("a.txt status = %+v, want Index=. Worktree=M", got)
	}
	if got := byPath["b.txt"]; got.Index != "?" || got.Worktree != "?" {
		t.Errorf("b.txt status = %+v, want Index=? Worktree=?", got)
	}
}

func TestExecuteGit_Diff(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "diff", Root: dir})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(result.Diff, "-hello") || !strings.Contains(result.Diff, "+hello world") {
		t.Errorf("diff = %q, want unified diff of a.txt", result.Diff)
	}

	result, err = executeGit(context.Background(), protocol.GitCommandPayload{Action: "diff", Root: dir, Staged: true})
	if err != nil {
		t.Fatalf("staged diff: %v", err)
	}
	if result.Diff != "" {
		t.Errorf("staged diff before staging = %q, want empty", result.Diff)
	}
}

func TestExecuteGit_Log(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "stage", Root: dir, Path: "a.txt"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "commit", Root: dir, Message: "first"}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "log", Root: dir, Limit: 10})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(result.Commits) != 1 {
		t.Fatalf("log = %+v, want one commit", result.Commits)
	}
	c := result.Commits[0]
	if len(c.SHA) != 40 || c.Subject != "first" || c.Author != "Test User" || c.Datetime.IsZero() {
		t.Errorf("commit = %+v", c)
	}
}

func TestExecuteGit_StageUnstageCommit(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "stage", Root: dir, Path: "a.txt"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status after stage: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Index != "A" || result.Files[0].Worktree != "." {
		t.Fatalf("status after stage = %+v, want one A/. entry", result.Files)
	}

	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "unstage", Root: dir, Path: "a.txt"}); err != nil {
		t.Fatalf("unstage: %v", err)
	}
	result, err = executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status after unstage: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Index != "?" || result.Files[0].Worktree != "?" {
		t.Fatalf("status after unstage = %+v, want one untracked entry", result.Files)
	}

	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "stage", Root: dir, Path: "a.txt"}); err != nil {
		t.Fatalf("re-stage: %v", err)
	}
	commitResult, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "commit", Root: dir, Message: "add a.txt"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(commitResult.Commit) != 40 {
		t.Errorf("commit sha = %q, want a 40-char hex sha", commitResult.Commit)
	}

	result, err = executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status after commit: %v", err)
	}
	if len(result.Files) != 0 {
		t.Errorf("status after commit = %+v, want clean", result.Files)
	}

	log := runSetupGitOutput(t, dir, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(log) != "add a.txt" {
		t.Errorf("commit message = %q", log)
	}
}

func runSetupGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestExecuteGit_CommitEmptyMessageRejected(t *testing.T) {
	dir := initGitRepo(t)
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "commit", Root: dir, Message: "   "}); err == nil {
		t.Fatal("commit with a blank message should fail")
	}
}

func TestExecuteGit_PathTraversalRejected(t *testing.T) {
	dir := initGitRepo(t)
	for _, action := range []string{"stage", "unstage"} {
		if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: action, Root: dir, Path: "../escape.txt"}); err == nil {
			t.Errorf("%s with a traversal path should fail", action)
		}
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "diff", Root: dir, Path: "../escape.txt"}); err == nil {
		t.Error("diff with a traversal path should fail")
	}
}

func TestExecuteGit_UnknownAction(t *testing.T) {
	dir := initGitRepo(t)
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "bogus", Root: dir}); err == nil {
		t.Fatal("unknown action should fail")
	}
}

func TestExecuteGit_MissingRoot(t *testing.T) {
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "status"}); err == nil {
		t.Fatal("missing root should fail")
	}
}

func TestExecuteGit_StatusReportsBranch(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	runSetupGit(t, dir, "branch", "-m", "main")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Branch == nil {
		t.Fatal("status result missing Branch")
	}
	if result.Branch.Name != "main" {
		t.Errorf("branch name = %q, want main", result.Branch.Name)
	}
	if result.Branch.Upstream != "" || result.Branch.Ahead != 0 || result.Branch.Behind != 0 {
		t.Errorf("branch = %+v, want no upstream on a repo with no remote", result.Branch)
	}
	if result.Branch.HasRemote {
		t.Errorf("branch.HasRemote = true, want false on a repo with no remote")
	}
}

// initGitRepoWithRemote sets up a local clone tracking a bare "origin",
// with one initial commit already pushed, for pull/push tests.
func initGitRepoWithRemote(t *testing.T) (dir, remoteDir string) {
	t.Helper()
	remoteDir = t.TempDir()
	runSetupGit(t, remoteDir, "init", "-q", "--bare")

	dir = t.TempDir()
	runSetupGit(t, dir, "init", "-q")
	runSetupGit(t, dir, "config", "user.name", "Test User")
	runSetupGit(t, dir, "config", "user.email", "test@example.com")
	runSetupGit(t, dir, "remote", "add", "origin", remoteDir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	runSetupGit(t, dir, "push", "-q", "-u", "origin", "HEAD")
	return dir, remoteDir
}

func TestExecuteGit_PushNoUpstreamSetsOne(t *testing.T) {
	dir, remoteDir := initGitRepoWithRemote(t)
	// A fresh local branch with no upstream configured.
	runSetupGit(t, dir, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "b.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "feature work")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "push", Root: dir})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !result.PushedNewUpstream {
		t.Error("PushedNewUpstream = false, want true for a first push")
	}

	clone := t.TempDir()
	runSetupGit(t, clone, "clone", "-q", remoteDir, ".")
	runSetupGit(t, clone, "rev-parse", "origin/feature")
}

func TestExecuteGit_PushExistingUpstream(t *testing.T) {
	dir, remoteDir := initGitRepoWithRemote(t)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "b.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "second")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "push", Root: dir})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if result.PushedNewUpstream {
		t.Error("PushedNewUpstream = true, want false for an existing upstream")
	}

	remoteLog := runSetupGitOutput(t, remoteDir, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(remoteLog) != "second" {
		t.Errorf("remote HEAD subject = %q, want %q", remoteLog, "second")
	}
}

func TestExecuteGit_PullFastForward(t *testing.T) {
	dir, remoteDir := initGitRepoWithRemote(t)

	other := t.TempDir()
	runSetupGit(t, other, "clone", "-q", remoteDir, ".")
	runSetupGit(t, other, "config", "user.name", "Other User")
	runSetupGit(t, other, "config", "user.email", "other@example.com")
	if err := os.WriteFile(filepath.Join(other, "c.txt"), []byte("from other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, other, "add", "c.txt")
	runSetupGit(t, other, "commit", "-q", "-m", "from other clone")
	runSetupGit(t, other, "push", "-q")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "pull", Root: dir})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("pull conflicts = %+v, want none", result.Conflicts)
	}
	if _, err := os.Stat(filepath.Join(dir, "c.txt")); err != nil {
		t.Errorf("c.txt not pulled in: %v", err)
	}
}

func TestExecuteGit_PullConflict(t *testing.T) {
	dir, remoteDir := initGitRepoWithRemote(t)

	other := t.TempDir()
	runSetupGit(t, other, "clone", "-q", remoteDir, ".")
	runSetupGit(t, other, "config", "user.name", "Other User")
	runSetupGit(t, other, "config", "user.email", "other@example.com")
	if err := os.WriteFile(filepath.Join(other, "a.txt"), []byte("from other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, other, "commit", "-qam", "conflicting change from other clone")
	runSetupGit(t, other, "push", "-q")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("from local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "commit", "-qam", "conflicting local change")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "pull", Root: dir})
	if err != nil {
		t.Fatalf("pull should surface conflicts, not an error: %v", err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].Path != "a.txt" {
		t.Fatalf("conflicts = %+v, want one entry for a.txt", result.Conflicts)
	}
}

func TestExecuteGit_RenameDetected(t *testing.T) {
	dir := initGitRepo(t)
	content := strings.Repeat("line of content\n", 20)
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "old.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")

	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "-A")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "status", Root: dir})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("status = %+v, want one renamed entry", result.Files)
	}
	if result.Files[0].Path != "new.txt" || result.Files[0].OrigPath != "old.txt" {
		t.Errorf("rename entry = %+v, want new.txt <- old.txt", result.Files[0])
	}
}

func TestExecuteGit_DiscardTrackedModified(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "discard", Root: dir, Path: "a.txt"}); err != nil {
		t.Fatalf("discard: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\n" {
		t.Errorf("a.txt content = %q, want restored", content)
	}

	// An untracked path is refused and left alone.
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "discard", Root: dir, Path: "b.txt"}); err == nil {
		t.Error("discard of an untracked path should be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Errorf("b.txt should still exist after the refused discard: %v", err)
	}

	// A clean path is refused.
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "discard", Root: dir, Path: "a.txt"}); err == nil {
		t.Error("discard of a clean path should be refused")
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "discard", Root: dir, Path: "../escape.txt"}); err == nil {
		t.Error("discard with a traversal path should fail")
	}
}

func TestExecuteGit_Amend(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	before := runSetupGitOutput(t, dir, "rev-parse", "HEAD")

	// Blank message refused (same rule as commit).
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "amend", Root: dir, Message: "  "}); err == nil {
		t.Error("amend with a blank message should fail")
	}

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "amend", Root: dir, Message: "amended subject"})
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if len(result.Commit) != 40 || result.Commit == strings.TrimSpace(before) {
		t.Errorf("amend commit = %q (before %q), want a new 40-char sha", result.Commit, before)
	}
	if subject := strings.TrimSpace(runSetupGitOutput(t, dir, "log", "-1", "--pretty=%s")); subject != "amended subject" {
		t.Errorf("amended subject = %q", subject)
	}

	// Once pushed, amend is refused (HEAD is an ancestor of the upstream).
	dirR, _ := initGitRepoWithRemote(t)
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "amend", Root: dirR, Message: "rewrite"}); err == nil {
		t.Error("amend after push should be refused")
	}
	// But HEAD ahead of the upstream amends fine.
	if err := os.WriteFile(filepath.Join(dirR, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dirR, "add", "b.txt")
	runSetupGit(t, dirR, "commit", "-q", "-m", "local only")
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "amend", Root: dirR, Message: "local-only amended"}); err != nil {
		t.Fatalf("amend of an unpushed commit: %v", err)
	}

	// An unborn repo (no HEAD): git's own error surfaces.
	empty := initGitRepo(t)
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "amend", Root: empty, Message: "x"}); err == nil {
		t.Error("amend with no HEAD should fail")
	}
}

func TestExecuteGit_BranchesCurrentAndUpstream(t *testing.T) {
	dir, _ := initGitRepoWithRemote(t)
	// initGitRepoWithRemote's push -u set the default branch's upstream; add
	// a second branch without one.
	runSetupGit(t, dir, "checkout", "-qb", "feature")
	runSetupGit(t, dir, "checkout", "-q", "master")

	result, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "branches", Root: dir})
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	current := 0
	byName := map[string]protocol.GitBranchInfo{}
	for _, b := range result.Branches {
		byName[b.Name] = b
		if !strings.Contains(b.Name, "/") {
			current++
		}
	}
	if current != 2 {
		t.Errorf("local branches = %d, want 2", current)
	}
	main, ok := byName["master"]
	if !ok {
		t.Fatalf("branches = %+v, want master", result.Branches)
	}
	if !main.Current || main.Upstream != "origin/master" {
		t.Errorf("master = %+v, want current with origin/master upstream", main)
	}
	feature, ok := byName["feature"]
	if !ok || feature.Current {
		t.Errorf("feature = %+v, want present and not current", feature)
	}
	if r, ok := byName["origin/master"]; !ok || !r.Remote {
		t.Errorf("remote origin/master = %+v, want present with Remote", r)
	}
	if main.Remote || feature.Remote {
		t.Error("local branches must not be flagged Remote")
	}
}

func TestExecuteGit_CheckoutAndBranchCreate(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetupGit(t, dir, "add", "a.txt")
	runSetupGit(t, dir, "commit", "-q", "-m", "initial")
	runSetupGit(t, dir, "branch", "-m", "main")

	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "branch", Root: dir, Path: "topic"}); err != nil {
		t.Fatalf("branch create: %v", err)
	}
	if head := strings.TrimSpace(runSetupGitOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); head != "topic" {
		t.Errorf("HEAD after branch create = %q", head)
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "checkout", Root: dir, Path: "main"}); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if head := strings.TrimSpace(runSetupGitOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); head != "main" {
		t.Errorf("HEAD after checkout = %q", head)
	}

	// Guards: empty/dash-prefixed names, duplicate create, dirty tree.
	for _, bad := range []string{"", "-b main"} {
		if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "branch", Root: dir, Path: bad}); err == nil {
			t.Errorf("branch %q should be refused", bad)
		}
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "branch", Root: dir, Path: "topic"}); err == nil {
		t.Error("re-creating an existing branch should fail")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "checkout", Root: dir, Path: "topic"}); err == nil {
		t.Error("checkout with a dirty tree should be refused")
	}
	// The refusal names the dirty count.
	if _, err := executeGit(context.Background(), protocol.GitCommandPayload{Action: "checkout", Root: dir, Path: "topic"}); err != nil && !strings.Contains(err.Error(), "working tree is dirty (1 changes)") {
		t.Errorf("checkout refusal = %v, want a dirty-count message", err)
	}
}
