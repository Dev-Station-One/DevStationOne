package backups

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/dbconnections"
	"devstationone/internal/projects"
	"devstationone/internal/runtimes"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DEVSTATIONONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSTATIONONE_TEST_DATABASE_URL not set; skipping integration test")
	}
	if err := database.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE backups, backup_schedules, database_connections, projects, runtimes, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var testDBEncryptionKey = []byte("0123456789abcdef0123456789abcdef")

var testSeq int

// testFixture seeds a user, runtime, project, and database connection —
// enough to exercise both backup kinds.
func testFixture(t *testing.T, pool *pgxpool.Pool) (userID, runtimeID, projectID, connectionID string) {
	t.Helper()
	ctx := context.Background()
	testSeq++
	u, err := auth.CreateUser(ctx, pool, fmt.Sprintf("owner%d@example.com", testSeq), "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	rt, _, err := runtimes.Create(ctx, pool, "dev01", u.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	p, err := projects.Create(ctx, pool, projects.Fields{
		Name: "App", RuntimeID: rt.ID, WorkingDirectory: "/home/dev/app", Shell: "/bin/bash",
	}, u.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	c, err := dbconnections.Create(ctx, pool, p.ID, dbconnections.Fields{
		Name: "primary", Engine: "sqlite", Database: "/tmp/test.db",
	}, dbconnections.SecretUpdate{}, testDBEncryptionKey, u.ID)
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return u.ID, rt.ID, p.ID, c.ID
}

func TestBackupCRUD(t *testing.T) {
	pool := testPool(t)
	userID, runtimeID, projectID, connectionID := testFixture(t, pool)
	ctx := context.Background()

	b, err := Create(ctx, pool, "db", runtimeID, "db/x.dump", "manual", userID, &connectionID, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.State != "running" || b.Trigger != "manual" {
		t.Fatalf("created backup = %+v", b)
	}

	if err := SetState(ctx, pool, b.ID, "done", 1234, ""); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, err := Get(ctx, pool, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "done" || got.SizeBytes != 1234 {
		t.Fatalf("got = %+v", got)
	}

	list, err := ListForConnection(ctx, pool, connectionID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}

	// A project-kind backup must not be visible in the connection's list.
	pb, err := Create(ctx, pool, "project", runtimeID, "projects/y.tar.gz", "manual", userID, nil, &projectID)
	if err != nil {
		t.Fatalf("create project backup: %v", err)
	}
	list, _ = ListForConnection(ctx, pool, connectionID)
	if len(list) != 1 {
		t.Fatalf("connection list leaked project backup: %+v", list)
	}
	projList, err := ListForProject(ctx, pool, projectID)
	if err != nil || len(projList) != 1 || projList[0].ID != pb.ID {
		t.Fatalf("project list = %+v, err = %v", projList, err)
	}

	fileName, gotRuntime, err := GetForDelete(ctx, pool, b.ID)
	if err != nil || fileName != "db/x.dump" || gotRuntime != runtimeID {
		t.Fatalf("GetForDelete = %q, %q, %v", fileName, gotRuntime, err)
	}
	if _, err := Get(ctx, pool, b.ID); err == nil {
		t.Fatal("backup still exists after GetForDelete")
	}
}

func TestBackupKindConstraint(t *testing.T) {
	pool := testPool(t)
	userID, runtimeID, projectID, connectionID := testFixture(t, pool)
	ctx := context.Background()

	// kind=db with a project_id set violates the CHECK constraint.
	if _, err := Create(ctx, pool, "db", runtimeID, "db/x.dump", "manual", userID, &connectionID, &projectID); err == nil {
		t.Fatal("expected constraint violation for db kind with project_id set")
	}
}

func TestScheduleCRUDAndDueSchedules(t *testing.T) {
	pool := testPool(t)
	userID, _, _, connectionID := testFixture(t, pool)
	ctx := context.Background()

	if _, err := GetScheduleForConnection(ctx, pool, connectionID); err != ErrNotFound {
		t.Fatalf("GetScheduleForConnection before create: err = %v, want ErrNotFound", err)
	}

	s, err := UpsertSchedule(ctx, pool, "db", "daily", 5, &connectionID, nil, userID)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if s.Cadence != "daily" || s.Retention != 5 {
		t.Fatalf("schedule = %+v", s)
	}
	if s.NextRunAt.Before(time.Now().Add(23 * time.Hour)) {
		t.Fatalf("NextRunAt = %v, want ~24h out", s.NextRunAt)
	}

	// Not due yet.
	due, err := DueSchedules(ctx, pool)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %+v, want none (next_run_at is a day out)", due)
	}

	// Force it due and confirm DueSchedules picks it up, then AdvanceSchedule
	// moves it back out of the due window.
	if _, err := pool.Exec(ctx, "UPDATE backup_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1", s.ID); err != nil {
		t.Fatalf("force due: %v", err)
	}
	due, err = DueSchedules(ctx, pool)
	if err != nil || len(due) != 1 || due[0].ID != s.ID {
		t.Fatalf("due = %+v, err = %v", due, err)
	}
	if err := AdvanceSchedule(ctx, pool, s.ID, "daily"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	due, err = DueSchedules(ctx, pool)
	if err != nil || len(due) != 0 {
		t.Fatalf("due after advance = %+v, err = %v", due, err)
	}

	if err := SetScheduleResult(ctx, pool, s.ID, "success", ""); err != nil {
		t.Fatalf("set result: %v", err)
	}
	s2, err := GetScheduleForConnection(ctx, pool, connectionID)
	if err != nil || s2.LastResult != "success" || s2.LastRunAt == nil {
		t.Fatalf("schedule after result = %+v, err = %v", s2, err)
	}

	// Upsert replaces cadence/retention in place (one schedule per
	// connection — ON CONFLICT, not a second row).
	s3, err := UpsertSchedule(ctx, pool, "db", "weekly", 20, &connectionID, nil, userID)
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if s3.ID != s.ID || s3.Cadence != "weekly" || s3.Retention != 20 {
		t.Fatalf("re-upserted schedule = %+v, want same id/weekly/20", s3)
	}

	found, err := DeleteSchedule(ctx, pool, "db", connectionID)
	if err != nil || !found {
		t.Fatalf("delete: found = %v, err = %v", found, err)
	}
	if _, err := GetScheduleForConnection(ctx, pool, connectionID); err != ErrNotFound {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestExcessScheduledBackupsRetention(t *testing.T) {
	pool := testPool(t)
	userID, runtimeID, _, connectionID := testFixture(t, pool)
	ctx := context.Background()

	// 5 scheduled + 1 manual backup; retention 3 should flag exactly the
	// oldest 2 scheduled ones, never the manual one.
	var scheduledIDs []string
	for i := 0; i < 5; i++ {
		b, err := Create(ctx, pool, "db", runtimeID, fmt.Sprintf("db/s%d.dump", i), "schedule", userID, &connectionID, nil)
		if err != nil {
			t.Fatalf("create scheduled backup %d: %v", i, err)
		}
		if err := SetState(ctx, pool, b.ID, "done", 1, ""); err != nil {
			t.Fatal(err)
		}
		scheduledIDs = append(scheduledIDs, b.ID)
		// created_at has second resolution in Postgres timestamptz here in
		// practice differs enough via insertion order + id tiebreak isn't
		// guaranteed, so stagger explicitly.
		if _, err := pool.Exec(ctx, "UPDATE backups SET created_at = now() - make_interval(secs => $2) WHERE id = $1", b.ID, (4-i)*10); err != nil {
			t.Fatalf("stagger created_at: %v", err)
		}
	}
	manual, err := Create(ctx, pool, "db", runtimeID, "db/manual.dump", "manual", userID, &connectionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetState(ctx, pool, manual.ID, "done", 1, ""); err != nil {
		t.Fatal(err)
	}

	ids, fileNames, err := ExcessScheduledBackups(ctx, pool, "db", connectionID, 3)
	if err != nil {
		t.Fatalf("excess: %v", err)
	}
	if len(ids) != 2 || len(fileNames) != 2 {
		t.Fatalf("excess = %d ids, want 2: %+v", len(ids), fileNames)
	}
	// The two oldest (highest stagger offset -> s0, s1) must be the ones
	// flagged for deletion.
	wantOldest := map[string]bool{scheduledIDs[0]: true, scheduledIDs[1]: true}
	for _, id := range ids {
		if !wantOldest[id] {
			t.Errorf("unexpected id flagged for deletion: %s", id)
		}
		if id == manual.ID {
			t.Error("manual backup flagged for deletion — must never be auto-trimmed")
		}
	}

	if err := DeleteRows(ctx, pool, ids); err != nil {
		t.Fatalf("delete rows: %v", err)
	}
	remaining, err := ListForConnection(ctx, pool, connectionID)
	if err != nil || len(remaining) != 4 {
		t.Fatalf("remaining after delete = %+v, err = %v", remaining, err)
	}
}
