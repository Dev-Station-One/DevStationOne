// Package audit records security-relevant events (docs/SECURITY.md "Audit"):
// authentication, runtime registration/revocation, project changes, terminal
// session lifecycle, database connection/principal/grant changes, service
// actions, and privileged filesystem operations. Recording is best-effort —
// a write failure is logged and never blocks the action it describes.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event types. The string values are the stored event_type column and the
// stable API/UI vocabulary — do not rename an existing constant's value.
const (
	EventLoginSucceeded       = "auth.login"
	EventLoginFailed          = "auth.login_failed"
	EventLoginTurnstileFailed = "auth.login_turnstile_failed"
	EventLogout               = "auth.logout"

	EventRuntimeDeleted     = "runtime.deleted"
	EventRuntimeRegistered  = "runtime.registered"
	EventRuntimeRevoked     = "runtime.revoked"
	EventRuntimeReactivated = "runtime.reactivated"

	EventProjectCreated = "project.created"
	EventProjectUpdated = "project.updated"
	EventProjectDeleted = "project.deleted"

	EventSessionCreated    = "session.created"
	EventSessionTerminated = "session.terminated"

	EventCommandCreated = "command.created"
	EventCommandUpdated = "command.updated"
	EventCommandDeleted = "command.deleted"

	EventDatabaseConnectionCreated = "database_connection.created"
	EventDatabaseConnectionUpdated = "database_connection.updated"
	EventDatabaseConnectionDeleted = "database_connection.deleted"

	EventDatabasePrincipalCreated         = "database_principal.created"
	EventDatabasePrincipalDeleted         = "database_principal.deleted"
	EventDatabasePrincipalPasswordChanged = "database_principal.password_changed"
	EventDatabaseGrantGranted             = "database_grant.granted"
	EventDatabaseGrantRevoked             = "database_grant.revoked"

	// EventServiceAction covers start/stop/restart/enable/disable; the
	// specific action is carried in Detail["action"] rather than one
	// constant per action, since the set of actions already exists
	// elsewhere (internal/api serviceActionHandler) and audit shouldn't
	// fork its own copy of that list.
	EventServiceAction = "service.action"

	EventFileCreated = "file.created"
	EventFileMkdir   = "file.mkdir"
	EventFileWritten = "file.written"
	// EventFileTransferred covers rename/copy/move; the specific action is
	// carried in Detail["action"] for the same reason as EventServiceAction.
	EventFileTransferred = "file.transferred"
	EventFileDeleted     = "file.deleted"
	EventFileChmod       = "file.chmod"
	EventFileChown       = "file.chown"

	// EventGitStaged/EventGitUnstaged cover a stage/unstage RPC; the path is
	// carried in Detail["path"].
	EventGitStaged    = "git.staged"
	EventGitUnstaged  = "git.unstaged"
	EventGitCommitted = "git.committed"
	EventGitPulled    = "git.pulled"
	EventGitPushed    = "git.pushed"

	EventJournalCreated         = "journal.created"
	EventJournalUpdated         = "journal.updated"
	EventJournalDeleted         = "journal.deleted"
	EventJournalAttachmentAdded = "journal.attachment_added"

	EventUserCreated = "user.created"
	EventUserUpdated = "user.updated"
	EventUserDeleted = "user.deleted"

	EventOwnershipTransferred = "ownership.transferred"

	// EventDockerAction covers container start/stop/restart/remove and
	// compose up/down/stop/restart; the specific action is carried in
	// Detail["action"] (same pattern as EventServiceAction).
	EventDockerAction = "docker.action"

	EventBackupCreated   = "backup.created"
	EventBackupRestored  = "backup.restored"
	EventBackupDeleted   = "backup.deleted"
	EventScheduleChanged = "backup_schedule.changed"
)

// Actor identifies who triggered an event. UserID is empty for an
// unauthenticated actor (a failed login attempt).
type Actor struct {
	UserID string
	Email  string
}

// Event is one recorded audit row.
type Event struct {
	ID           string
	OccurredAt   time.Time
	EventType    string
	ActorUserID  *string
	ActorEmail   string
	ResourceType string
	ResourceID   string
	Detail       map[string]any
	IPAddress    string
}

const eventColumns = `id, occurred_at, event_type, actor_user_id, actor_email, resource_type, resource_id, detail, ip_address`

func scanEvent(row pgx.Row) (Event, error) {
	var e Event
	var detail []byte
	err := row.Scan(&e.ID, &e.OccurredAt, &e.EventType, &e.ActorUserID, &e.ActorEmail, &e.ResourceType, &e.ResourceID, &detail, &e.IPAddress)
	if err != nil {
		return Event{}, err
	}
	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &e.Detail); err != nil {
			return Event{}, err
		}
	}
	return e, nil
}

// Record inserts one audit event. Best-effort: a write failure is logged
// via the default slog logger and swallowed, so a broken audit log never
// blocks the security-relevant action it is describing.
func Record(ctx context.Context, pool *pgxpool.Pool, eventType string, actor Actor, resourceType, resourceID string, detail map[string]any, ipAddress string) {
	if detail == nil {
		detail = map[string]any{}
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		slog.Default().Error("audit: failed to marshal detail", "event_type", eventType, "error", err)
		return
	}
	var actorID *string
	if actor.UserID != "" {
		actorID = &actor.UserID
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO audit_events (event_type, actor_user_id, actor_email, resource_type, resource_id, detail, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
	`, eventType, actorID, actor.Email, resourceType, resourceID, detailJSON, ipAddress)
	if err != nil {
		slog.Default().Error("audit: failed to record event", "event_type", eventType, "error", err)
	}
}

// Filter narrows List/Count. An empty EventType/ActorUserID/ResourceType or
// a nil Since/Until leaves that dimension unfiltered.
type Filter struct {
	EventType    string
	ActorUserID  string
	ResourceType string
	Since        *time.Time
	Until        *time.Time
	Limit        int
	Offset       int
}

const filterWhere = `
	WHERE ($1 = '' OR event_type = $1)
	  AND ($2 = '' OR actor_user_id::text = $2)
	  AND ($3 = '' OR resource_type = $3)
	  AND ($4::timestamptz IS NULL OR occurred_at >= $4)
	  AND ($5::timestamptz IS NULL OR occurred_at <= $5)
`

func filterArgs(f Filter) []any {
	return []any{f.EventType, f.ActorUserID, f.ResourceType, f.Since, f.Until}
}

// List returns events matching f, most recent first, alongside the total
// count of matching rows (ignoring Limit/Offset) for pagination.
func List(ctx context.Context, pool *pgxpool.Pool, f Filter) ([]Event, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events `+filterWhere, filterArgs(f)...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args := append(filterArgs(f), limit, f.Offset)
	rows, err := pool.Query(ctx, `SELECT `+eventColumns+` FROM audit_events `+filterWhere+` ORDER BY occurred_at DESC LIMIT $6 OFFSET $7`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ErrNotFound is unused today (the settings row is seeded by migration and
// never deleted) but kept for the same not-found convention every other
// internal package follows.
var ErrNotFound = errors.New("not found")

// Settings is the single-row audit retention policy/configuration.
type Settings struct {
	RetentionDays int
	UpdatedAt     time.Time
}

// GetSettings returns the current retention policy.
func GetSettings(ctx context.Context, pool *pgxpool.Pool) (Settings, error) {
	var s Settings
	err := pool.QueryRow(ctx, `SELECT retention_days, updated_at FROM audit_settings WHERE id = 1`).Scan(&s.RetentionDays, &s.UpdatedAt)
	if err != nil {
		return Settings{}, err
	}
	return s, nil
}

// UpdateSettings sets the retention policy. retentionDays <= 0 means keep
// audit events forever (Purge becomes a no-op).
func UpdateSettings(ctx context.Context, pool *pgxpool.Pool, retentionDays int) (Settings, error) {
	var s Settings
	err := pool.QueryRow(ctx, `
		UPDATE audit_settings SET retention_days = $1, updated_at = now() WHERE id = 1
		RETURNING retention_days, updated_at
	`, retentionDays).Scan(&s.RetentionDays, &s.UpdatedAt)
	if err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Purge deletes audit events older than the configured retention policy and
// returns how many rows were removed. A retention_days <= 0 means keep
// forever, so Purge is a no-op.
func Purge(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	s, err := GetSettings(ctx, pool)
	if err != nil {
		return 0, err
	}
	if s.RetentionDays <= 0 {
		return 0, nil
	}
	tag, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE occurred_at < now() - make_interval(days => $1)`, s.RetentionDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
