// Package commandruns records one row per Quick Command run, with the
// runner, timing, exit status, and the bounded output tail captured at exit
// (ADR-045 — the one deliberate exception to ADR-007's never-persist rule,
// scoped to command-run sessions only).
package commandruns

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching run.
var ErrNotFound = errors.New("not found")

// tailLimit is how much of a run's PTY output is kept — the full captured
// output, capped at 100 MiB as a safety net (ADR-045; the same cap the
// Hub's in-memory capture uses).
const tailLimit = 100 * 1024 * 1024

// runsPerPage caps ?limit= on the history list, and the per-project prune
// keeps at most this many newest runs.
const runsPerPage = 100

// Run is one recorded Quick Command run. Name and CommandText snapshot the
// command at run time — the command row itself can still change after the
// run. Deleting the command deletes its runs too (ON DELETE CASCADE): a
// command's history has no reason to outlive the command itself.
type Run struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	CommandID   string `json:"command_id"`
	Name        string `json:"name"`
	CommandText string `json:"command"`
	// SessionID is NULL until the run handler opens the run's PTY session
	// (and back to NULL if the session row is ever deleted).
	SessionID  *string    `json:"session_id,omitempty"`
	RunBy      string     `json:"run_by"`
	RunByName  string     `json:"run_by_name"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	OutputTail string     `json:"output_tail,omitempty"`
}

// runCore is the plain command_runs column list, aliased r.
const runCore = `r.id, r.project_id, r.command_id, r.name, r.command_text, r.session_id, r.run_by,
	r.started_at, r.finished_at, r.exit_code, r.error, r.output_tail`

// runColumns appends the runner's display name (email fallback), matching
// internal/journal's creatorNameExpr.
const runColumns = runCore + `,
	COALESCE(NULLIF(u.display_name, ''), u.email) AS run_by_name`

func scanRun(row pgx.Row) (Run, error) {
	var run Run
	err := row.Scan(&run.ID, &run.ProjectID, &run.CommandID, &run.Name, &run.CommandText, &run.SessionID,
		&run.RunBy, &run.StartedAt, &run.FinishedAt, &run.ExitCode, &run.Error, &run.OutputTail, &run.RunByName)
	return run, err
}

// Start inserts a run row in its running state (finished_at NULL) and
// best-effort prunes the project's history to the newest 100 runs.
func Start(ctx context.Context, pool *pgxpool.Pool, projectID, commandID, name, commandText, runBy string) (Run, error) {
	run, err := scanRun(pool.QueryRow(ctx, `
		INSERT INTO command_runs AS r (project_id, command_id, name, command_text, run_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+runCore+`,
			(SELECT COALESCE(NULLIF(display_name, ''), email) FROM users WHERE id = r.run_by) AS run_by_name`,
		projectID, commandID, name, commandText, runBy,
	))
	if err != nil {
		return Run{}, err
	}
	// History is for self-audit, not forever-storage: keep the newest 100.
	_, _ = pool.Exec(ctx, `
		DELETE FROM command_runs WHERE project_id = $1 AND id NOT IN
			(SELECT id FROM command_runs WHERE project_id = $1 ORDER BY started_at DESC, id DESC LIMIT 100)`,
		projectID)
	return run, nil
}

// SetSession links a run row to the PTY session the run handler opened for
// it, so the session.exit handler can finalize the run by session_id.
func SetSession(ctx context.Context, pool *pgxpool.Pool, runID, sessionID string) error {
	_, err := pool.Exec(ctx, `UPDATE command_runs SET session_id = $2 WHERE id = $1`, runID, sessionID)
	return err
}

// FinalizeSession marks the run row for sessionID finished, recording its
// exit status and the captured output tail. It is a no-op for sessions with
// no run row (ordinary terminals) and idempotent against an already-final
// row. exitCode nil means the session failed without an exit code.
func FinalizeSession(ctx context.Context, pool *pgxpool.Pool, sessionID string, exitCode *int, errMsg, tail string) error {
	_, err := pool.Exec(ctx, `
		UPDATE command_runs SET finished_at = now(), exit_code = $2, error = $3, output_tail = $4
		WHERE session_id = $1 AND finished_at IS NULL
	`, sessionID, exitCode, errMsg, tail)
	return err
}

// FailRun finalizes by run ID, for open failures that happen before the run
// row could ever be linked to a session.
func FailRun(ctx context.Context, pool *pgxpool.Pool, runID, errMsg string) error {
	_, err := pool.Exec(ctx, `
		UPDATE command_runs SET finished_at = now(), error = $2 WHERE id = $1 AND finished_at IS NULL
	`, runID, errMsg)
	return err
}

// ListForProject returns a page of a project's runs, optionally scoped to
// one command, newest first, with the output tail left empty (the list view
// never renders it — the detail fetch does).
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID, commandID string, limit, offset int) ([]Run, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	where := `WHERE r.project_id = $1`
	args := []any{projectID}
	if commandID != "" {
		args = append(args, commandID)
		where += ` AND r.command_id = $2`
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM command_runs r `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := pool.Query(ctx, `SELECT `+runColumns+`
		FROM command_runs r JOIN users u ON u.id = r.run_by
		`+where+` ORDER BY r.started_at DESC LIMIT $`+fmtIt(len(args)+1)+` OFFSET $`+fmtIt(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		run.OutputTail = ""
		out = append(out, run)
	}
	return out, total, rows.Err()
}

// GetForProject looks up one run and verifies it belongs to projectID.
func GetForProject(ctx context.Context, pool *pgxpool.Pool, projectID, id string) (Run, error) {
	run, err := scanRun(pool.QueryRow(ctx, `SELECT `+runColumns+`
		FROM command_runs r JOIN users u ON u.id = r.run_by
		WHERE r.id = $1 AND r.project_id = $2`, id, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

// fmtIt renders a 1-based SQL parameter index.
func fmtIt(n int) string { return strconv.Itoa(n) }

// ansiRe strips CSI sequences (colors, cursor moves) from a captured tail —
// the history viewer renders plain text, not a terminal.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b[=>]`)

// Tail converts raw PTY bytes into the text stored as a run's output_tail:
// the last 64 KiB, ANSI-stripped and forced to valid UTF-8 (PTY bytes can be
// anything; the text column and JSON cannot).
func Tail(data []byte) string {
	if len(data) > tailLimit {
		data = data[len(data)-tailLimit:]
	}
	s := ansiRe.ReplaceAllString(strings.ToValidUTF8(string(data), "�"), "")
	return strings.ReplaceAll(s, "\r", "")
}
