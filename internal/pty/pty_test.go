package pty

import (
	"bytes"
	"os"
	"os/user"
	"syscall"
	"testing"
	"time"
)

func readUntil(t *testing.T, s *Session, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out bytes.Buffer
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := s.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			if bytes.Contains(out.Bytes(), []byte(want)) {
				return out.String()
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not see %q within %s; got %q", want, timeout, out.String())
	return ""
}

func TestSession_RunsRealShellCommand(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if _, err := s.Write([]byte("echo hello-from-pty\nexit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	readUntil(t, s, "hello-from-pty", 5*time.Second)

	if code := s.Wait(); code != 0 {
		t.Fatalf("Wait() = %d, want 0", code)
	}
}

func TestSession_Resize(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if err := s.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if _, err := s.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	s.Wait()
}

func TestSession_Signal(t *testing.T) {
	s, err := Start(Options{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if _, err := s.Write([]byte("sleep 30\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let sh fork sleep before we signal it

	if err := s.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Signal: %v", err)
	}

	done := make(chan int, 1)
	go func() { done <- s.Wait() }()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatalf("Wait() = %d after SIGKILL, want nonzero", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after SIGKILL")
	}
}

// unsetTermEnv removes TERM from the test process's environment, matching
// the real runtime deployment (a systemd user service, which has no TERM).
func unsetTermEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("TERM")
	os.Unsetenv("TERM")
	if had {
		t.Cleanup(func() { os.Setenv("TERM", old) })
	}
}

// Sessions must start with color-capable TERM/COLORTERM defaults so TUI
// apps render color: the runtime (systemd user service) provides neither.
func TestStart_SetsColorTerminalDefaults(t *testing.T) {
	unsetTermEnv(t)

	s, err := Start(Options{Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if _, err := s.Write([]byte("echo $TERM; echo $COLORTERM\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := readUntil(t, s, "xterm-256color", 5*time.Second)
	if !bytes.Contains([]byte(out), []byte("truecolor")) {
		// Split across PTY reads; both values arrive back-to-back if so.
		readUntil(t, s, "truecolor", 5*time.Second)
	}
	s.Write([]byte("exit\n"))
	s.Wait()
}

// An explicit TERM from the Project's environment wins over the defaults,
// COLORTERM included — respect the user's terminal choice as given.
func TestStart_RespectsExplicitTerm(t *testing.T) {
	unsetTermEnv(t)

	s, err := Start(Options{
		Shell: "/bin/sh",
		Env:   map[string]string{"TERM": "screen-256color"},
		Cols:  80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if _, err := s.Write([]byte("echo $TERM; echo ${COLORTERM:-unset}\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := readUntil(t, s, "screen-256color", 5*time.Second)
	if !bytes.Contains([]byte(out), []byte("unset")) {
		t.Fatalf("COLORTERM was set to %q; explicit TERM must not add it", out)
	}
	s.Write([]byte("exit\n"))
	s.Wait()
}

func TestStart_RejectsMismatchedOSUser(t *testing.T) {
	if _, err := Start(Options{Shell: "/bin/sh", OSUser: "definitely-not-a-real-user"}); err == nil {
		t.Fatal("expected Start to reject a mismatched os_user")
	}
}

func TestStart_AcceptsMatchingOSUser(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	s, err := Start(Options{Shell: "/bin/sh", OSUser: cur.Username})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()
	s.Write([]byte("exit\n"))
	s.Wait()
}
