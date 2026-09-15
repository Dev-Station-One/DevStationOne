package backups

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/dbconnections"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// pollInterval is how often the scheduler looks for due schedules and how
// often an in-flight job's status is polled.
const pollInterval = 3 * time.Second

// RunScheduler fires due backup schedules until ctx is done. It is the
// Control Server process's background loop (started from main.go). At most
// one process runs it in a deployment: DueSchedules uses FOR UPDATE SKIP
// LOCKED so a second instance would simply never see due rows.
func RunScheduler(ctx context.Context, pool *pgxpool.Pool, registry *runtimes.Registry, dbKey []byte, logger *slog.Logger) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			due, err := DueSchedules(ctx, pool)
			if err != nil {
				logger.Error("backup scheduler query failed", "error", err)
				continue
			}
			for _, s := range due {
				// Advance first (crash-safe: a failed run is recorded, not
				// retried this tick), then run the job synchronously — one
				// long backup delays later schedules by its duration, which
				// is acceptable for the handful a real deployment has.
				if err := AdvanceSchedule(ctx, pool, s.ID, s.Cadence); err != nil {
					logger.Error("advance schedule failed", "schedule_id", s.ID, "error", err)
					continue
				}
				runScheduledBackup(ctx, pool, registry, dbKey, logger, s)
			}
		}
	}
}

// runScheduledBackup fires one schedule's backup and records the outcome.
func runScheduledBackup(ctx context.Context, pool *pgxpool.Pool, registry *runtimes.Registry, dbKey []byte, logger *slog.Logger, s Schedule) {
	var (
		runtimeID string
		kindDir   string
		conn      runtimes.Conn
		online    bool
	)
	if s.Kind == "db" {
		c, err := dbconnections.Get(ctx, pool, *s.ConnectionID)
		if err != nil {
			SetScheduleResult(ctx, pool, s.ID, "failed", "connection not found: "+err.Error())
			return
		}
		p, err := projects.Get(ctx, pool, c.ProjectID)
		if err != nil {
			SetScheduleResult(ctx, pool, s.ID, "failed", "connection's project not found")
			return
		}
		runtimeID, kindDir = p.RuntimeID, "db"
	} else {
		p, err := projects.Get(ctx, pool, *s.ProjectID)
		if err != nil {
			SetScheduleResult(ctx, pool, s.ID, "failed", "project not found")
			return
		}
		runtimeID, kindDir = p.RuntimeID, "projects"
	}
	conn, online = registry.Get(runtimeID)
	if !online {
		SetScheduleResult(ctx, pool, s.ID, "skipped_offline", "")
		return
	}

	backupID := uuid.NewString()
	fileName := kindDir + "/" + backupID
	if kindDir == "db" {
		fileName += ".dump"
	} else {
		fileName += ".tar.gz"
	}
	rowKind := s.Kind
	createdBy := s.CreatedBy
	row, err := Create(ctx, pool, rowKind, runtimeID, fileName, "schedule", createdBy, s.ConnectionID, s.ProjectID)
	if err != nil {
		SetScheduleResult(ctx, pool, s.ID, "failed", "failed to record backup row")
		return
	}

	jobID, err := startBackup(ctx, conn, s, dbKey, pool, backupID, fileName)
	if err != nil {
		SetState(ctx, pool, row.ID, "failed", 0, err.Error())
		SetScheduleResult(ctx, pool, s.ID, "failed", err.Error())
		return
	}
	state, size, jobErr := AwaitJob(ctx, conn, jobID)
	if state != "done" {
		if state == "" {
			state, jobErr = "failed", "runtime disconnected during backup"
		}
		SetState(ctx, pool, row.ID, "failed", 0, jobErr)
		SetScheduleResult(ctx, pool, s.ID, "failed", jobErr)
		logger.Warn("scheduled backup failed", "schedule_id", s.ID, "error", jobErr)
		return
	}
	SetState(ctx, pool, row.ID, "done", size, "")
	SetScheduleResult(ctx, pool, s.ID, "success", "")
	applyRetention(ctx, pool, conn, s.Kind, refIDOf(s), s.Retention)
}

func refIDOf(s Schedule) string {
	if s.ConnectionID != nil {
		return *s.ConnectionID
	}
	return *s.ProjectID
}

// startBackup issues the start_db/start_project command for a schedule.
func startBackup(ctx context.Context, conn runtimes.Conn, s Schedule, dbKey []byte, pool *pgxpool.Pool, backupID, fileName string) (string, error) {
	cmd := protocol.BackupCommandPayload{BackupID: backupID}
	if s.Kind == "db" {
		c, err := dbconnections.Get(ctx, pool, *s.ConnectionID)
		if err != nil {
			return "", err
		}
		p, err := dbconnections.ResolveParams(ctx, pool, c.ID, dbKey)
		if err != nil {
			return "", err
		}
		cp := toProtoParams(p)
		cmd.Action, cmd.Params = "start_db", &cp
	} else {
		p, err := projects.Get(ctx, pool, *s.ProjectID)
		if err != nil {
			return "", err
		}
		cmd.Action, cmd.SourceDir = "start_project", p.WorkingDirectory
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := conn.SendCommand(ctxTimeout, protocol.MessageBackupCommand, cmd)
	if err != nil {
		return "", err
	}
	var result protocol.BackupResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.JobID, nil
}

// awaitJob polls a backup job's status until done/failed.
// AwaitJob polls a backup job's status until done/failed. Returns state "" if
// the runtime disconnected before completion.
func AwaitJob(ctx context.Context, conn runtimes.Conn, jobID string) (state string, size int64, errMsg string) {
	for {
		raw, err := conn.SendCommand(ctx, protocol.MessageBackupCommand, protocol.BackupCommandPayload{Action: "status", JobID: jobID})
		if err != nil {
			return "", 0, ""
		}
		var result protocol.BackupResultPayload
		if err := json.Unmarshal(raw, &result); err == nil {
			switch result.State {
			case "done":
				return "done", result.Size, ""
			case "failed":
				return "failed", 0, result.Error
			}
		}
		select {
		case <-ctx.Done():
			return "", 0, ""
		case <-time.After(pollInterval):
		}
	}
}

// applyRetention trims a schedule's oldest done backups beyond its
// retention count, deleting the runtime-side files first, then the rows.
func applyRetention(ctx context.Context, pool *pgxpool.Pool, conn runtimes.Conn, kind, refID string, retention int) {
	ids, fileNames, err := ExcessScheduledBackups(ctx, pool, kind, refID, retention)
	if err != nil {
		return // best-effort; next successful run trims again
	}
	for _, fn := range fileNames {
		raw, err := conn.SendCommand(ctx, protocol.MessageBackupCommand, protocol.BackupCommandPayload{Action: "delete", FileName: fn})
		_ = raw
		if err != nil {
			return // runtime problem; rows stay, retry next run
		}
	}
	_ = DeleteRows(ctx, pool, ids)
}

// toProtoParams converts decrypted connection params to the protocol shape.
// Kept here (a copy of api.toDBConnectionParams) because the scheduler
// can't import the api package.
func toProtoParams(p dbconnections.Params) protocol.DBConnectionParams {
	return protocol.DBConnectionParams{
		Engine: p.Engine, Host: p.Host, Port: p.Port, Username: p.Username, Password: p.Password,
		Database: p.Database, SSLMode: p.SSLMode, InstanceName: p.InstanceName, SearchPath: p.SearchPath,
		ExtraParams: p.ExtraParams,
	}
}
