package commands

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
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
	if _, err := pool.Exec(context.Background(),
		"TRUNCATE command_runs, project_commands, terminal_sessions, projects, runtimes, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var testUserSeq int64

func testProject(t *testing.T, pool *pgxpool.Pool) (projectID, userID string) {
	t.Helper()
	ctx := context.Background()
	testUserSeq++
	email := "commander-" + strconv.FormatInt(testUserSeq, 10) + "@example.com"
	u, err := auth.CreateUser(ctx, pool, email, "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtimes (name, token_hash, status, created_by)
		VALUES ($2, $3, 'online', $1)`, u.ID, "dev01-"+strconv.FormatInt(testUserSeq, 10), "commands-test-hash-"+strconv.FormatInt(testUserSeq, 10)); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		VALUES ('Commands Proj', (SELECT id FROM runtimes LIMIT 1), '/tmp', '/bin/bash', '{}', $1)`,
		u.ID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM projects ORDER BY created_at DESC LIMIT 1`).Scan(&projectID); err != nil {
		t.Fatalf("read project: %v", err)
	}
	return projectID, u.ID
}

func TestCRUD(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, userID := testProject(t, pool)

	f := Fields{Name: "Build", Command: "make build", SortOrder: 0}
	c, err := Create(ctx, pool, projectID, f, userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Name != "Build" || c.Command != "make build" || c.ProjectID != projectID {
		t.Fatalf("created = %+v", c)
	}

	second, err := Create(ctx, pool, projectID, Fields{Name: "Test", Command: "make test", SortOrder: 1}, userID)
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	list, err := ListForProject(ctx, pool, projectID)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(list) != 2 || list[0].ID != c.ID || list[1].ID != second.ID {
		t.Fatalf("list = %+v", list)
	}

	// Project scoping: another project's ID must not see the command.
	otherProject, _ := testProject(t, pool)
	if _, err := GetForProject(ctx, pool, otherProject, c.ID); err != ErrNotFound {
		t.Fatalf("cross-project GetForProject = %v, want ErrNotFound", err)
	}

	got, err := GetForProject(ctx, pool, projectID, c.ID)
	if err != nil {
		t.Fatalf("GetForProject: %v", err)
	}
	if got.Name != "Build" {
		t.Fatalf("got %+v", got)
	}

	updated, err := Update(ctx, pool, c.ID, Fields{Name: "Build all", Command: "make build all", SortOrder: 5})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Build all" || updated.SortOrder != 5 {
		t.Fatalf("updated = %+v", updated)
	}

	if err := Delete(ctx, pool, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := GetForProject(ctx, pool, projectID, c.ID); err != ErrNotFound {
		t.Fatalf("post-delete Get = %v, want ErrNotFound", err)
	}
}
