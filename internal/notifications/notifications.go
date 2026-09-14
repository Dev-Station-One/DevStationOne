// Package notifications records per-user notifications (task completion,
// awaiting-input, ...) and pushes them live to connected browsers over the
// Slice 14 events.Bus (docs/IMPLEMENTATION_PLAN.md Slice 15). It mirrors
// internal/audit's shape and best-effort recording convention, but is
// scoped per-user with a read flag instead of being a system-wide log.
package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/events"
)

// Notification types. The string values are the stored type column and the
// stable API/UI vocabulary — do not rename an existing constant's value.
const (
	TypeTaskCompleted = "task.completed"
	TypeAwaitingInput = "session.awaiting_input"
)

// Notification is one stored, per-user notification. Json tags double as
// the wire shape for both the REST list response and the "notification.
// created" live event payload — there is no separate DTO, unlike
// internal/audit, since no field needs hiding or reshaping for either
// consumer.
type Notification struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Detail    map[string]any `json:"detail"`
	ReadAt    *time.Time     `json:"read_at"`
	CreatedAt time.Time      `json:"created_at"`
}

const columns = `id, user_id, type, title, body, detail, read_at, created_at`

func scan(row pgx.Row) (Notification, error) {
	var n Notification
	var detail []byte
	if err := row.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &detail, &n.ReadAt, &n.CreatedAt); err != nil {
		return Notification{}, err
	}
	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &n.Detail); err != nil {
			return Notification{}, err
		}
	}
	return n, nil
}

// Record inserts a notification for userID and publishes it on bus as
// "notification.created" (scoped to userID) so an open dashboard updates
// live. Best-effort, like internal/audit.Record: a write failure is logged
// and never blocks the action it describes (task completion, idle
// detection).
func Record(ctx context.Context, pool *pgxpool.Pool, bus *events.Bus, userID, ntype, title, body string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		slog.Default().Error("notifications: failed to marshal detail", "type", ntype, "error", err)
		return
	}

	n, err := scan(pool.QueryRow(ctx, `
		INSERT INTO notifications (user_id, type, title, body, detail)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING `+columns,
		userID, ntype, title, body, detailJSON,
	))
	if err != nil {
		slog.Default().Error("notifications: failed to record", "type", ntype, "error", err)
		return
	}

	bus.Publish(events.Event{Type: "notification.created", UserID: &n.UserID, Payload: n})
}

// ListForUser returns userID's notifications, most recent first, alongside
// the total count of matching rows (ignoring limit/offset) for pagination.
func ListForUser(ctx context.Context, pool *pgxpool.Pool, userID string, limit, offset int) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := pool.Query(ctx, `SELECT `+columns+` FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// UnreadCount returns how many of userID's notifications are unread.
func UnreadCount(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkRead marks one of userID's notifications read, returning false if no
// such notification exists (wrong ID, or owned by another user). Idempotent:
// marking an already-read notification again leaves its read_at unchanged
// and still reports success.
func MarkRead(ctx context.Context, pool *pgxpool.Pool, userID, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, now()) WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkAllRead marks all of userID's unread notifications read, returning how
// many were affected.
func MarkAllRead(ctx context.Context, pool *pgxpool.Pool, userID string) (int64, error) {
	tag, err := pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
