// Package sessions manages persistent terminal (PTY) session records: their
// lifecycle status, and the in-memory routing between attached browser
// WebSocket connections and the Development Runtime that owns each PTY.
package sessions

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching session.
var ErrNotFound = errors.New("not found")

// Status values a terminal_sessions row can hold, matching the lifecycle in
// docs/PROTOCOL.md plus a "failed" terminal state for a session.open that
// never started.
const (
	StatusRequested = "requested"
	StatusStarting  = "starting"
	StatusRunning   = "running"
	StatusDetached  = "detached"
	StatusExited    = "exited"
	StatusFailed    = "failed"
	StatusClosed    = "closed"
)

// Session is a stored PTY session: its Project, lifecycle status, and
// terminal dimensions. PTY bytes themselves are never persisted (ADR-007).
type Session struct {
	ID           string
	ProjectID    string
	Name         string
	Status       string
	Cols         int
	Rows         int
	ExitCode     *int
	ErrorMessage string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
	RenderMode   string
}

const sessionColumns = `id, project_id, name, status, cols, rows, exit_code, error_message, created_by, created_at, updated_at, closed_at, render_mode`

func scanSession(row pgx.Row) (Session, error) {
	var s Session
	var errMsg *string
	err := row.Scan(&s.ID, &s.ProjectID, &s.Name, &s.Status, &s.Cols, &s.Rows, &s.ExitCode, &errMsg, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ClosedAt, &s.RenderMode)
	if err != nil {
		return Session{}, err
	}
	if errMsg != nil {
		s.ErrorMessage = *errMsg
	}
	return s, nil
}

// Create inserts a new terminal session in StatusRequested. renderMode is
// "xterm" or "cards" (Slice 17) — callers resolve an empty/omitted request
// field to "xterm" before calling Create.
func Create(ctx context.Context, pool *pgxpool.Pool, projectID, name string, cols, rows int, createdByUserID, renderMode string) (Session, error) {
	return scanSession(pool.QueryRow(ctx, `
		INSERT INTO terminal_sessions (project_id, name, cols, rows, created_by, render_mode)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+sessionColumns,
		projectID, name, cols, rows, createdByUserID, renderMode,
	))
}

// ListForProject returns a project's sessions, most recently created first.
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]Session, error) {
	rows, err := pool.Query(ctx, `SELECT `+sessionColumns+` FROM terminal_sessions WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ActiveCountsByProjectIDs returns how many non-terminal sessions each of
// the given projects currently has, keyed by project ID. A project with no
// active sessions is simply absent from the result (zero value on lookup).
func ActiveCountsByProjectIDs(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) (map[string]int, error) {
	out := make(map[string]int)
	if len(projectIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT project_id, count(*) FROM terminal_sessions
		WHERE project_id = ANY($1) AND status NOT IN ($2, $3, $4)
		GROUP BY project_id
	`, projectIDs, StatusExited, StatusFailed, StatusClosed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Get looks up a session by ID. Callers are responsible for authorizing
// access via the session's Project.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM terminal_sessions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// Rename updates a session's display name.
func Rename(ctx context.Context, pool *pgxpool.Pool, id, name string) (Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `
		UPDATE terminal_sessions SET name = $2, updated_at = now() WHERE id = $1
		RETURNING `+sessionColumns, id, name,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

// MarkStarting transitions a session to StatusStarting, right after
// session.open is sent to the runtime.
func MarkStarting(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `UPDATE terminal_sessions SET status = $2, updated_at = now() WHERE id = $1`, id, StatusStarting)
	return err
}

// MarkDetached transitions a session to StatusDetached: its process is
// alive but no browser is currently attached.
func MarkDetached(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `
		UPDATE terminal_sessions SET status = $2, updated_at = now()
		WHERE id = $1 AND status NOT IN ($3, $4, $5)
	`, id, StatusDetached, StatusExited, StatusFailed, StatusClosed)
	return err
}

// MarkRunning transitions a session to StatusRunning: its process is alive
// and a browser is currently attached.
func MarkRunning(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `
		UPDATE terminal_sessions SET status = $2, updated_at = now()
		WHERE id = $1 AND status NOT IN ($3, $4, $5)
	`, id, StatusRunning, StatusExited, StatusFailed, StatusClosed)
	return err
}

// MarkFailed transitions a session to StatusFailed with an error message,
// used when session.open never produces a running process.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id, errMsg string) error {
	_, err := pool.Exec(ctx, `
		UPDATE terminal_sessions SET status = $2, error_message = $3, updated_at = now(), closed_at = now()
		WHERE id = $1
	`, id, StatusFailed, errMsg)
	return err
}

// MarkExited transitions a session to StatusExited, recording the process
// exit code. Guarded against StatusClosed: an explicit close races the
// process's own asynchronous exit event (killing it to close also makes it
// exit), and the explicit close must win.
func MarkExited(ctx context.Context, pool *pgxpool.Pool, id string, exitCode int) error {
	_, err := pool.Exec(ctx, `
		UPDATE terminal_sessions SET status = $2, exit_code = $3, updated_at = now(), closed_at = now()
		WHERE id = $1 AND status != $4
	`, id, StatusExited, exitCode, StatusClosed)
	return err
}

// MarkClosed transitions a session to StatusClosed, the terminal state for
// an explicit close request.
func MarkClosed(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `
		UPDATE terminal_sessions SET status = $2, updated_at = now(), closed_at = now()
		WHERE id = $1
	`, id, StatusClosed)
	return err
}

// UpdateSize persists the last-known terminal dimensions.
func UpdateSize(ctx context.Context, pool *pgxpool.Pool, id string, cols, rows int) error {
	_, err := pool.Exec(ctx, `UPDATE terminal_sessions SET cols = $2, rows = $3, updated_at = now() WHERE id = $1`, id, cols, rows)
	return err
}
