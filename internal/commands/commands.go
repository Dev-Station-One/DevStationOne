// Package commands stores a Project's Quick Commands (ADR-045): named CLI
// commands a project member can run with one click, each opening a real PTY
// session on the project's runtime (see internal/commandruns for the run
// history, and the import parsers in this package).
package commands

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching command.
var ErrNotFound = errors.New("not found")

// Command is one stored Quick Command.
type Command struct {
	ID        string
	ProjectID string
	Name      string
	Command   string
	SortOrder int
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Fields groups the user-supplied properties of a Command, used for both
// creation and updates.
type Fields struct {
	Name      string
	Command   string
	SortOrder int
}

const commandColumns = `id, project_id, name, command, sort_order, COALESCE(created_by::text, '') AS created_by, created_at, updated_at`

func scanCommand(row pgx.Row) (Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Command, &c.SortOrder, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// Create inserts a new Quick Command on projectID, owned by createdByUserID.
func Create(ctx context.Context, pool *pgxpool.Pool, projectID string, f Fields, createdByUserID string) (Command, error) {
	return scanCommand(pool.QueryRow(ctx, `
		INSERT INTO project_commands (project_id, name, command, sort_order, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+commandColumns,
		projectID, f.Name, f.Command, f.SortOrder, createdByUserID,
	))
}

// ListForProject returns a project's commands in UI order.
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]Command, error) {
	rows, err := pool.Query(ctx, `SELECT `+commandColumns+` FROM project_commands WHERE project_id = $1 ORDER BY sort_order, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetForProject looks up a command and verifies it belongs to projectID.
func GetForProject(ctx context.Context, pool *pgxpool.Pool, projectID, id string) (Command, error) {
	c, err := scanCommand(pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM project_commands WHERE id = $1 AND project_id = $2`, id, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Command{}, ErrNotFound
	}
	if err != nil {
		return Command{}, err
	}
	return c, nil
}

// Update overwrites a command's fields.
func Update(ctx context.Context, pool *pgxpool.Pool, id string, f Fields) (Command, error) {
	return scanCommand(pool.QueryRow(ctx, `
		UPDATE project_commands SET name = $2, command = $3, sort_order = $4, updated_at = now()
		WHERE id = $1
		RETURNING `+commandColumns,
		id, f.Name, f.Command, f.SortOrder,
	))
}

// Delete removes a command and its run history (command_runs.command_id is
// ON DELETE CASCADE) — a command's history has no reason to outlive it.
func Delete(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM project_commands WHERE id = $1`, id)
	return err
}
