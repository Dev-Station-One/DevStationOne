package pty

import (
	"strings"
	"testing"
	"time"
)

// TestStart_WithCommand runs `shell -c <command>` to exit (ADR-045): output
// arrives, the process exits on its own with the command's exit code, and
// the empty-Command path is unchanged (covered by pty_test.go above).
func TestStart_WithCommand(t *testing.T) {
	// -l -i: 'i' shows up in $- (interactive → ~/.bashrc sourced, where
	// user PATH additions like nvm live); the login chain itself is
	// proven by `shopt -q login_shell` — login state is not in $-.
	s, err := Start(Options{Shell: "/bin/bash", Command: "echo flags:$-; shopt -q login_shell && echo LOGIN-OK; exit 0", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	got := readUntil(t, s, "flags:", 5*time.Second)

	if code := s.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(got, "i") || !strings.Contains(got, "flags:") {
		t.Fatalf("output %q: shell is not interactive (-i missing)", got)
	}
	if !strings.Contains(got, "LOGIN-OK") {
		t.Fatalf("output %q: shell is not a login shell (-l missing)", got)
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
