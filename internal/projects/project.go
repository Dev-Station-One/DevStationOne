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
	Description      string
	RuntimeID        string
	WorkingDirectory string
	Shell            string
	Environment      map[string]string
	CreatedBy        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// LastAccessedAt is when a session was last opened on this project;
	// nil for projects never opened since the column existed.
	LastAccessedAt *time.Time
	// OwnerName is the creating user's display name (email fallback),
	// resolved by the ownerSubquery column in every SELECT below.
	OwnerName string
}

// Fields groups the user-supplied properties of a Project, used for both
// creation and updates.
type Fields struct {
	Name             string
	Description      string
	RuntimeID        string
	WorkingDirectory string
	Shell            string
	Environment      map[string]string
}

// ownerNameExpr resolves a created_by reference to a display name
// (email fallback); same pattern as internal/journal's creatorNameExpr.
const ownerNameExpr = `COALESCE(NULLIF(display_name, ''), email)`

const projectColumns = `id, name, description, runtime_id, working_directory, shell, environment, created_by, created_at, updated_at, last_accessed_at, ` +
	`(SELECT ` + ownerNameExpr + ` FROM users WHERE id = created_by) AS owner_name`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	var env []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.RuntimeID, &p.WorkingDirectory, &p.Shell, &env, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.LastAccessedAt, &p.OwnerName)
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
		INSERT INTO projects (name, description, runtime_id, working_directory, shell, environment, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
		RETURNING `+projectColumns,
		f.Name, f.Description, f.RuntimeID, f.WorkingDirectory, f.Shell, env, createdByUserID,
	))
	if err != nil {
		return Project{}, err
	}
	return p, nil
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

// GetByRuntimeRoot looks up the project bound to runtimeID's root working
// directory. The Code tab's runtime-side file-change pushes carry only the
// runtime ID and absolute root, so the Control Server maps that pair back to
// the project whose editor streams the change belongs to.
func GetByRuntimeRoot(ctx context.Context, pool *pgxpool.Pool, runtimeID, root string) (Project, error) {
	p, err := scanProject(pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE runtime_id = $1 AND working_directory = $2`, runtimeID, root))
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
			description = $3,
			runtime_id = $4,
			working_directory = $5,
			shell = $6,
			environment = $7::jsonb,
			updated_at = now()
		WHERE id = $1
		RETURNING `+projectColumns,
		id, f.Name, f.Description, f.RuntimeID, f.WorkingDirectory, f.Shell, env,
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

// TouchAccessed stamps the project's last-accessed time. A failure here is
// harmless metadata loss, so callers may ignore the error.
func TouchAccessed(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `UPDATE projects SET last_accessed_at = now() WHERE id = $1`, id)
	return err
}

func marshalEnvironment(env map[string]string) ([]byte, error) {
	if env == nil {
		env = map[string]string{}
	}
	return json.Marshal(env)
}
