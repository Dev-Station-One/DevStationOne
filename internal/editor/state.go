package editor

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a user+project has no stored editor state.
var ErrNotFound = errors.New("not found")

// GetState returns the stored editor tab-state JSON for userID's view of
// projectID, or ErrNotFound when nothing is stored yet. The server treats
// the state as an opaque JSON object — its shape (open tabs, active tab,
// cursor offsets) belongs to the browser.
func GetState(ctx context.Context, pool *pgxpool.Pool, userID, projectID string) ([]byte, error) {
	var state []byte
	err := pool.QueryRow(ctx, `SELECT state FROM project_editor_state WHERE user_id = $1 AND project_id = $2`, userID, projectID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

// PutState upserts userID's editor tab-state JSON for projectID.
func PutState(ctx context.Context, pool *pgxpool.Pool, userID, projectID string, state []byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO project_editor_state (user_id, project_id, state)
		VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (user_id, project_id) DO UPDATE SET state = $3::jsonb, updated_at = now()
	`, userID, projectID, state)
	return err
}
