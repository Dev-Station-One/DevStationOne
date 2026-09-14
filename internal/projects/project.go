// Package projects manages Project execution-context records: which
// runtime, OS user, working directory, shell, and environment a Project's
// sessions run with.
package projects

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching project.
var ErrNotFound = errors.New("not found")

// Project is a stored execution context: the runtime, working directory,
// shell, and environment a Project's sessions run with. The OS user is not
// stored here — a runtime always runs as one fixed OS user (ADR-014), so
// it's reported live via runtimes.Runtime.Capabilities.OSUsername instead.
type Project struct {
	ID               string
	Name             string
	RuntimeID        string
	WorkingDirectory string
	Shell            string
	Environment      map[string]string
	CreatedBy        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Fields groups the user-supplied properties of a Project, used for both
// creation and updates.
type Fields struct {
	Name             string
	RuntimeID        string
	WorkingDirectory string
	Shell            string
	Environment      map[string]string
}

const projectColumns = `id, name, runtime_id, working_directory, shell, environment, created_by, created_at, updated_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	var env []byte
	err := row.Scan(&p.ID, &p.Name, &p.RuntimeID, &p.WorkingDirectory, &p.Shell, &env, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	if len(env) > 0 {
		if err := json.Unmarshal(env, &p.Environment); err != nil {
			return Project{}, err
		}
	}
	return p, nil
}

// Create inserts a new project owned by createdByUserID.
func Create(ctx context.Context, pool *pgxpool.Pool, f Fields, createdByUserID string) (Project, error) {
	env, err := marshalEnvironment(f.Environment)
	if err != nil {
		return Project{}, err
	}
	p, err := scanProject(pool.QueryRow(ctx, `
		INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING `+projectColumns,
		f.Name, f.RuntimeID, f.WorkingDirectory, f.Shell, env, createdByUserID,
	))
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// ListForUser returns projects owned by userID, most recently created first.
func ListForUser(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Project, error) {
	return list(ctx, pool, `WHERE created_by = $1`, userID)
}

// ListAll returns every project, most recently created first.
func ListAll(ctx context.Context, pool *pgxpool.Pool) ([]Project, error) {
	return list(ctx, pool, ``)
}

func list(ctx context.Context, pool *pgxpool.Pool, where string, args ...any) ([]Project, error) {
	rows, err := pool.Query(ctx, `SELECT `+projectColumns+` FROM projects `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get looks up a project by ID regardless of owner. Callers are responsible
// for authorizing access to the result.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Project, error) {
	p, err := scanProject(pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// Update overwrites a project's fields and reports whether a matching row
// was found.
func Update(ctx context.Context, pool *pgxpool.Pool, id string, f Fields) (Project, error) {
	env, err := marshalEnvironment(f.Environment)
	if err != nil {
		return Project{}, err
	}
	p, err := scanProject(pool.QueryRow(ctx, `
		UPDATE projects SET
			name = $2,
			runtime_id = $3,
			working_directory = $4,
			shell = $5,
			environment = $6::jsonb,
			updated_at = now()
		WHERE id = $1
		RETURNING `+projectColumns,
		id, f.Name, f.RuntimeID, f.WorkingDirectory, f.Shell, env,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// Delete removes a project and reports whether a matching row was found.
func Delete(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func marshalEnvironment(env map[string]string) ([]byte, error) {
	if env == nil {
		env = map[string]string{}
	}
	return json.Marshal(env)
}
