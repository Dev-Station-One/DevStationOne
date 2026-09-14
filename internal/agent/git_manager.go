package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/filesystem"
	"devstationone/internal/protocol"
)

// gitCommandTimeout bounds a single git operation.
const gitCommandTimeout = 30 * time.Second

// gitManager implements the git.command half of connection.Manager: it runs
// the real git CLI (docs/DECISIONS.md ADR-028 — no shell, argv only, direct
// os/exec) inside a Project's authorized root directory, and replies with
// the same correlation ID the request carried.
type gitManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender
}

func newGitManager(logger *slog.Logger) *gitManager {
	return &gitManager{logger: logger}
}

func (m *gitManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *gitManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one git.command message. It runs the operation in
// its own goroutine so a slow call never blocks the connection's read loop.
func (m *gitManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageGitCommand {
		return
	}
	var cmd protocol.GitCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *gitManager) run(id string, cmd protocol.GitCommandPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()

	result, err := executeGit(ctx, cmd)
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageGitError, protocol.ServiceErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send git error reply failed", "action", cmd.Action, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageGitResult, result); replyErr != nil {
		m.logger.Warn("send git result reply failed", "action", cmd.Action, "error", replyErr)
	}
}

func executeGit(ctx context.Context, cmd protocol.GitCommandPayload) (protocol.GitResultPayload, error) {
	if cmd.Root == "" {
		return protocol.GitResultPayload{}, fmt.Errorf("missing root directory")
	}

	switch cmd.Action {
	case "status":
		// Scoped to Root via a "." pathspec rather than a bare `git status`,
		// in case Root is a subdirectory of a larger git checkout — without
		// it, a repo rooted above Root would leak paths/changes outside the
		// Project's authorized directory into the result.
		out, err := runGit(ctx, cmd.Root, "", "status", "--porcelain=v2", "-z", "--", ".")
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{Files: parseGitStatus(out)}, nil

	case "diff":
		p, err := gitDiffPath(cmd.Path)
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		args := []string{"diff"}
		if cmd.Staged {
			args = append(args, "--cached")
		}
		args = append(args, "--", p)
		out, err := runGit(ctx, cmd.Root, "", args...)
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{Diff: out}, nil

	case "stage":
		p, err := filesystem.Clean(cmd.Path)
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		if _, err := runGit(ctx, cmd.Root, "", "add", "--", p); err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{}, nil

	case "unstage":
		p, err := filesystem.Clean(cmd.Path)
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		// "git restore --staged" refuses to run in a repo with no commits
		// yet ("could not resolve HEAD"); "git reset --" is the classic
		// unstage command and handles that case (an implicit empty tree).
		if _, err := runGit(ctx, cmd.Root, "", "reset", "--", p); err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{}, nil

	case "commit":
		msg := strings.TrimSpace(cmd.Message)
		if msg == "" {
			return protocol.GitResultPayload{}, fmt.Errorf("commit message is required")
		}
		if _, err := runGit(ctx, cmd.Root, cmd.Message, "commit", "-F", "-"); err != nil {
			return protocol.GitResultPayload{}, err
		}
		sha, err := runGit(ctx, cmd.Root, "", "rev-parse", "HEAD")
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{Commit: strings.TrimSpace(sha)}, nil

	case "log":
		limit := cmd.Limit
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		out, err := runGit(ctx, cmd.Root, "", "log", "--max-count="+strconv.Itoa(limit),
			"--pretty=format:%H%x1f%s%x1f%an%x1f%aI", "--date=iso-strict")
		if err != nil {
			return protocol.GitResultPayload{}, err
		}
		return protocol.GitResultPayload{Commits: parseGitLog(out)}, nil

	default:
		return protocol.GitResultPayload{}, fmt.Errorf("unknown git action %q", cmd.Action)
	}
}

// gitDiffPath resolves an optional diff path into a git pathspec: "" (no
// path given) scopes to the whole Project root rather than the whole
// repository, same containment reasoning as "status".
func gitDiffPath(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	return filesystem.Clean(p)
}

// runGit executes git as a direct child process (no shell — argv only,
// docs/SECURITY.md) with dir as its working directory, feeding stdin when
// non-empty (used by "commit -F -" so the message never appears as a
// process argument). It returns stdout, folding stderr into the error on
// failure.
func runGit(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// parseGitStatus parses `git status --porcelain=v2 -z` output into a flat
// change list. See git-status(1) "Porcelain Format Version 2": an ordinary
// entry ("1 ...") or unmerged entry ("u ...") is one NUL-terminated record
// ending in its path; a rename/copy entry ("2 ...") ends in its new path the
// same way, but is followed by one more NUL-terminated record carrying the
// original path; untracked entries ("? ...") are one record.
func parseGitStatus(out string) []protocol.GitFileStatus {
	tokens := strings.Split(out, "\x00")
	var files []protocol.GitFileStatus
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" {
			continue
		}
		switch tok[0] {
		case '1':
			if f := splitFields(tok, 9); f != nil {
				files = append(files, statusFromXY(f[1], f[8]))
			}
		case '2':
			if f := splitFields(tok, 10); f != nil && i+1 < len(tokens) {
				s := statusFromXY(f[1], f[9])
				s.OrigPath = tokens[i+1]
				files = append(files, s)
				i++
			}
		case 'u':
			if f := splitFields(tok, 11); f != nil {
				files = append(files, statusFromXY(f[1], f[10]))
			}
		case '?':
			if f := splitFields(tok, 2); f != nil {
				files = append(files, protocol.GitFileStatus{Path: f[1], Index: "?", Worktree: "?"})
			}
		}
	}
	return files
}

// splitFields splits a porcelain v2 record into exactly n space-separated
// fields, the last of which may itself contain spaces (a path). Returns nil
// if the record has fewer than n fields.
func splitFields(tok string, n int) []string {
	f := strings.SplitN(tok, " ", n)
	if len(f) < n {
		return nil
	}
	return f
}

func statusFromXY(xy, path string) protocol.GitFileStatus {
	x, y := ".", "."
	if len(xy) == 2 {
		x, y = string(xy[0]), string(xy[1])
	}
	return protocol.GitFileStatus{Path: path, Index: x, Worktree: y}
}

// parseGitLog parses `git log --pretty=format:%H%x1f%s%x1f%an%x1f%aI`
// output: one 0x1f-separated record per line, newest first. A repo with
// no commits yields empty output (git log fails there with a HEAD error,
// which runGit surfaces as an error the caller maps to a 502).
func parseGitLog(out string) []protocol.GitCommitInfo {
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var commits []protocol.GitCommitInfo
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 4 {
			continue
		}
		when, err := time.Parse(time.RFC3339, f[3])
		if err != nil {
			continue
		}
		commits = append(commits, protocol.GitCommitInfo{SHA: f[0], Subject: f[1], Author: f[2], Datetime: when})
	}
	return commits
}
