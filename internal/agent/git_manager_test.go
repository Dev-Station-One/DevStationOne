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
