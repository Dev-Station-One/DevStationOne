// Package backups owns backup metadata (Postgres rows describing backup
// files that physically live on runtime hosts — ADR-041), the schedules
// that trigger them, and the scheduler goroutine that fires due schedules
// (ADR-042: jobs run on the runtime and are polled for completion).
package backups

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// Valid cadences and their intervals.
var cadenceIntervals = map[string]time.Duration{
	"hourly": time.Hour,
	"daily":  24 * time.Hour,
	"weekly": 7 * 24 * time.Hour,
}

// Backup is one stored backup: metadata for a file on a runtime host.
type Backup struct {
	ID           string
	Kind         string // db | project
	ConnectionID *string
	ProjectID    *string
	RuntimeID    string
	FileName     string
	SizeBytes    int64
	Trigger      string // manual | schedule | pre_restore
	State        string // running | done | failed
	Error        string
	CreatedBy    string
	CreatedAt    time.Time
}

const backupColumns = `id, kind, connection_id, project_id, runtime_id, file_name, size_bytes, trigger, state, COALESCE(error, ''), created_by, created_at`

func scanBackup(row pgx.Row) (Backup, error) {
	var b Backup
	err := row.Scan(&b.ID, &b.Kind, &b.ConnectionID, &b.ProjectID, &b.RuntimeID, &b.FileName,
		&b.SizeBytes, &b.Trigger, &b.State, &b.Error, &b.CreatedBy, &b.CreatedAt)
	return b, err
}

// Create inserts a backup row in state running.
func Create(ctx context.Context, pool *pgxpool.Pool, kind, runtimeID, fileName, trigger, createdBy string, connectionID, projectID *string) (Backup, error) {
	b, err := scanBackup(pool.QueryRow(ctx, `
		INSERT INTO backups (kind, connection_id, project_id, runtime_id, file_name, trigger, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+backupColumns,
		kind, connectionID, projectID, runtimeID, fileName, trigger, createdBy))
	return b, err
}

// Get looks up one backup row.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Backup, error) {
	b, err := scanBackup(pool.QueryRow(ctx, `SELECT `+backupColumns+` FROM backups WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	return b, err
}

// ListForConnection returns a connection's backups, newest first.
func ListForConnection(ctx context.Context, pool *pgxpool.Pool, connectionID string) ([]Backup, error) {
	return listWhere(ctx, pool, `WHERE connection_id = $1`, connectionID)
}

// ListForProject returns a project's backups, newest first.
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]Backup, error) {
	return listWhere(ctx, pool, `WHERE project_id = $1`, projectID)
}

func listWhere(ctx context.Context, pool *pgxpool.Pool, where string, arg any) ([]Backup, error) {
	rows, err := pool.Query(ctx, `SELECT `+backupColumns+` FROM backups `+where+` ORDER BY created_at DESC`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetState updates a backup row's size/state/error as its job progresses.
func SetState(ctx context.Context, pool *pgxpool.Pool, id string, state string, size int64, errMsg string) error {
	_, err := pool.Exec(ctx, `
		UPDATE backups SET size_bytes = $2, state = $3, error = NULLIF($4, '')
		WHERE id = $1`, id, size, state, errMsg)
	return err
}

// GetForDelete returns the row's file name and runtime, then removes the
// row — the caller deletes the file on that runtime.
func GetForDelete(ctx context.Context, pool *pgxpool.Pool, id string) (fileName, runtimeID string, err error) {
	err = pool.QueryRow(ctx, `DELETE FROM backups WHERE id = $1 RETURNING file_name, runtime_id`, id).Scan(&fileName, &runtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return fileName, runtimeID, err
}

// Schedule is one per-connection or per-project backup schedule.
type Schedule struct {
	ID           string
	Kind         string // db | project
	ConnectionID *string
	ProjectID    *string
	Cadence      string // hourly | daily | weekly
	Retention    int
	NextRunAt    time.Time
	LastRunAt    *time.Time
	LastResult   string // success | failed | skipped_offline
	LastError    string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const scheduleColumns = `id, kind, connection_id, project_id, cadence, retention, next_run_at, last_run_at, COALESCE(last_result, ''), COALESCE(last_error, ''), created_by, created_at, updated_at`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.Kind, &s.ConnectionID, &s.ProjectID, &s.Cadence, &s.Retention,
		&s.NextRunAt, &s.LastRunAt, &s.LastResult, &s.LastError, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// UpsertSchedule creates or replaces the schedule for a connection or
// project (exactly one must be non-nil). nextRunAt is now() + the cadence.
func UpsertSchedule(ctx context.Context, pool *pgxpool.Pool, kind, cadence string, retention int, connectionID, projectID *string, createdBy string) (Schedule, error) {
	interval, ok := cadenceIntervals[cadence]
	if !ok {
		return Schedule{}, errors.New("invalid cadence")
	}
	next := time.Now().Add(interval)
	// The two kinds have separate UNIQUE columns, so each kind gets its own
	// ON CONFLICT target.
	conflictCol := "connection_id"
	if kind == "project" {
		conflictCol = "project_id"
	}
	return scanSchedule(pool.QueryRow(ctx, `
		INSERT INTO backup_schedules (kind, connection_id, project_id, cadence, retention, next_run_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (`+conflictCol+`) DO UPDATE SET
			cadence = EXCLUDED.cadence,
			retention = EXCLUDED.retention,
			next_run_at = EXCLUDED.next_run_at,
			last_result = NULL,
			last_error = NULL,
			updated_at = now()
		RETURNING `+scheduleColumns,
		kind, connectionID, projectID, cadence, retention, next, createdBy))
}

// GetScheduleForConnection returns a connection's schedule, if any.
func GetScheduleForConnection(ctx context.Context, pool *pgxpool.Pool, connectionID string) (Schedule, error) {
	return getScheduleWhere(ctx, pool, `WHERE connection_id = $1`, connectionID)
}

// GetScheduleForProject returns a project's schedule, if any.
func GetScheduleForProject(ctx context.Context, pool *pgxpool.Pool, projectID string) (Schedule, error) {
	return getScheduleWhere(ctx, pool, `WHERE project_id = $1`, projectID)
}

func getScheduleWhere(ctx context.Context, pool *pgxpool.Pool, where string, arg any) (Schedule, error) {
	s, err := scanSchedule(pool.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM backup_schedules `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	return s, err
}

// DeleteSchedule removes a connection's or project's schedule, reporting
// whether one existed.
func DeleteSchedule(ctx context.Context, pool *pgxpool.Pool, kind, refID string) (bool, error) {
	col := "connection_id"
	if kind == "project" {
		col = "project_id"
	}
	tag, err := pool.Exec(ctx, `DELETE FROM backup_schedules WHERE `+col+` = $1`, refID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DueSchedules returns schedules whose next_run_at has passed, locking them
// against concurrent tickers (SKIP LOCKED — only one server process wins).
func DueSchedules(ctx context.Context, pool *pgxpool.Pool) ([]Schedule, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+scheduleColumns+` FROM backup_schedules
		WHERE next_run_at <= now()
		ORDER BY next_run_at
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AdvanceSchedule moves a schedule's next_run_at past the current interval
// (called at fire time, so a failed run doesn't retry immediately) and
// stamps last_run_at.
func AdvanceSchedule(ctx context.Context, pool *pgxpool.Pool, id, cadence string) error {
	interval, ok := cadenceIntervals[cadence]
	if !ok {
		return errors.New("invalid cadence")
	}
	_, err := pool.Exec(ctx, `
		UPDATE backup_schedules
		SET next_run_at = $2, last_run_at = now(), updated_at = now()
		WHERE id = $1`, id, time.Now().Add(interval))
	return err
}

// SetScheduleResult records a run's outcome.
func SetScheduleResult(ctx context.Context, pool *pgxpool.Pool, id, result, errMsg string) error {
	_, err := pool.Exec(ctx, `
		UPDATE backup_schedules SET last_result = $2, last_error = NULLIF($3, ''), updated_at = now()
		WHERE id = $1`, id, result, errMsg)
	return err
}

// ExcessScheduledBackups returns the ids and file names of the oldest
// schedule-triggered backups beyond a retention count, for deletion.
func ExcessScheduledBackups(ctx context.Context, pool *pgxpool.Pool, kind, refID string, retention int) (ids, fileNames []string, err error) {
	col := "connection_id"
	if kind == "project" {
		col = "project_id"
	}
	rows, err := pool.Query(ctx, `
		SELECT id, file_name FROM (
			SELECT id, file_name, row_number() OVER (ORDER BY created_at DESC) AS rn
			FROM backups
			WHERE `+col+` = $1 AND trigger = 'schedule' AND state = 'done'
		) ranked WHERE rn > $2`, refID, retention)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, fn string
		if err := rows.Scan(&id, &fn); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		fileNames = append(fileNames, fn)
	}
	return ids, fileNames, rows.Err()
}

// DeleteRows removes backup metadata rows by id (their files were already
// deleted on the runtime, or never materialized).
func DeleteRows(ctx context.Context, pool *pgxpool.Pool, ids []string) error {
	_, err := pool.Exec(ctx, `DELETE FROM backups WHERE id = ANY($1)`, ids)
	return err
}
