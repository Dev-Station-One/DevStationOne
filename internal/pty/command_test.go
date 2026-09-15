package pty

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestStart_WithCommand runs `shell -i -c <command>` to exit (ADR-045):
// output arrives, the process exits with the command's exit code, the
// shell is interactive (so ~/.bashrc sources — the actual bug this fixed:
// -l made it a login shell, which reads the profile chain INSTEAD of
// .bashrc and skipped a real user's PATH additions there), and NOT a
// login shell (shopt login_shell must be off, or ~/.bashrc gets skipped
// again). The empty-Command path is unchanged (covered by pty_test.go).
func TestStart_WithCommand(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/bash", Command: "echo flags:$-; shopt -q login_shell && echo LOGIN || echo NOT-LOGIN; exit 0", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	got := readUntil(t, s, "flags:", 5*time.Second)

	if code := s.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(got, "flags:") || !strings.Contains(got, "i") {
		t.Fatalf("output %q: shell is not interactive (-i missing)", got)
	}
	if !strings.Contains(got, "NOT-LOGIN") {
		t.Fatalf("output %q: shell is a login shell — ~/.bashrc would be skipped (-l must not be passed)", got)
	}
}

// TestStart_EmptyCommandKeepsInteractiveShell guards the pre-ADR-045
// behavior: an empty Command starts the plain shell, which stays alive
// until told to exit.
func TestStart_EmptyCommandKeepsInteractiveShell(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if _, err := s.Write([]byte("echo still-interactive\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	readUntil(t, s, "still-interactive", 5*time.Second)

	if _, err := s.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write exit: %v", err)
	}
	if code := s.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// TestStart_WithCommand_SourcesBashrcPathAdditions is the regression test
// for the actual bug: a fixture HOME with only a ~/.bashrc PATH addition
// (no ~/.profile, no ~/.bash_profile — same shape as the real host this
// broke on) must be visible to the command. `-l` would read the profile
// chain instead and never see it.
func TestStart_WithCommand_SourcesBashrcPathAdditions(t *testing.T) {
	home := t.TempDir()
	binDir := home + "/fakebin"
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	toolPath := binDir + "/quickcmd-tool"
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\necho TOOL-FOUND\n"), 0o755); err != nil {
		t.Fatalf("write fake tool: %v", err)
	}
	bashrc := "export PATH=\"" + binDir + ":$PATH\"\n"
	if err := os.WriteFile(home+"/.bashrc", []byte(bashrc), 0o644); err != nil {
		t.Fatalf("write .bashrc: %v", err)
	}

	s, err := Start(Options{
		Shell:   "/bin/bash",
		Command: "quickcmd-tool; exit $?",
		Env:     map[string]string{"HOME": home},
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	got := readUntil(t, s, "TOOL-FOUND", 5*time.Second)
	if code := s.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0; output %q", code, got)
	}
}
