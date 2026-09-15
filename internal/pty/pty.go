// Package pty starts and controls real OS pseudo-terminal sessions on the
// Development Runtime (ADR-006: interactive sessions must use real PTYs,
// not command/request simulation).
package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"syscall"

	creackpty "github.com/creack/pty"
)

// Options configures a new PTY-backed process.
type Options struct {
	// Shell is the absolute path to the binary to execute.
	Shell string
	// Dir is the process's working directory.
	Dir string
	// OSUser is the OS user the Project expects its sessions to run as.
	// The runtime has no privilege-escalation path (it runs as a normal
	// user, never root — ADR-014), so this must match the runtime
	// process's own user; Start refuses to silently run as whichever user
	// happens to be invoking it if the two disagree.
	OSUser string
	// Env is merged on top of the runtime process's own environment.
	Env  map[string]string
	Cols uint16
	Rows uint16
	// Command optionally runs `shell -c <command>` instead of an
	// interactive shell prompt (Quick Commands, ADR-045). Empty = the plain
	// shell, Start's only behavior before ADR-045.
	Command string
}

// Session is one running PTY-backed process.
type Session struct {
	cmd *exec.Cmd
	f   *os.File
}

// Start launches opts.Shell attached to a new PTY sized Cols x Rows.
func Start(opts Options) (*Session, error) {
	if opts.OSUser != "" {
		cur, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("determine runtime OS user: %w", err)
		}
		if cur.Username != opts.OSUser {
			return nil, fmt.Errorf("project os_user %q does not match the runtime's OS user %q", opts.OSUser, cur.Username)
		}
	}

	var cmd *exec.Cmd
	if opts.Command != "" {
		// -l -i: source the login chain (profile → bashrc) and run
		// interactively, so the command sees the same env (nvm, PATH
		// additions in ~/.bashrc) as a session typed into from term.js
		// (ADR-045). Non-interactive `shell -c` skips ~/.bashrc entirely.
		cmd = exec.Command(opts.Shell, "-l", "-i", "-c", opts.Command)
	} else {
		cmd = exec.Command(opts.Shell)
	}
	cmd.Dir = opts.Dir
	cmd.Env = mergeEnv(os.Environ(), opts.Env)

	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Rows: opts.Rows, Cols: opts.Cols})
	if err != nil {
		return nil, err
	}
	return &Session{cmd: cmd, f: f}, nil
}

// Read reads process output from the PTY.
func (s *Session) Read(p []byte) (int, error) { return s.f.Read(p) }

// Write sends input bytes to the PTY.
func (s *Session) Write(p []byte) (int, error) { return s.f.Write(p) }

// Resize changes the PTY's terminal dimensions.
func (s *Session) Resize(cols, rows uint16) error {
	return creackpty.Setsize(s.f, &creackpty.Winsize{Rows: rows, Cols: cols})
}

// Signal delivers a POSIX signal to the process.
func (s *Session) Signal(sig syscall.Signal) error {
	if s.cmd.Process == nil {
		return errors.New("process not started")
	}
	return s.cmd.Process.Signal(sig)
}

// Wait blocks until the process exits and returns its exit code (-1 if it
// could not be determined, e.g. the process was killed by a signal).
func (s *Session) Wait() int {
	err := s.cmd.Wait()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// Close releases the PTY file descriptor and kills the process if it is
// still running.
func (s *Session) Close() error {
	_ = s.f.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
