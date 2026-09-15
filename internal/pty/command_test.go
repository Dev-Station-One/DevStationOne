package pty

import (
	"testing"
	"time"
)

// TestStart_WithCommand runs `shell -c <command>` to exit (ADR-045): output
// arrives, the process exits on its own with the command's exit code, and
// the empty-Command path is unchanged (covered by pty_test.go above).
func TestStart_WithCommand(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/sh", Command: "echo cmd-ran-quick; exit 7", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	readUntil(t, s, "cmd-ran-quick", 5*time.Second)

	if code := s.Wait(); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
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
