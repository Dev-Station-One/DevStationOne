package dbconnections

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
	"devstationone/internal/database"
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE projects, runtimes, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testProject(t *testing.T, pool *pgxpool.Pool) (projectID, userID string) {
	t.Helper()
	u, err := auth.CreateUser(context.Background(), pool, "dbconn-test@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	rt, _, err := runtimes.Create(context.Background(), pool, "dev01", u.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	p, err := projects.Create(context.Background(), pool, projects.Fields{
		Name: "proj", RuntimeID: rt.ID, WorkingDirectory: "/home/dev", Shell: "/bin/bash",
	}, u.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID, u.ID
}

func TestCreateGetListDelete(t *testing.T) {
	pool := testPool(t)
	projectID, userID := testProject(t, pool)
	key := testKey()

	c, err := Create(context.Background(), pool, projectID, Fields{
		Name: "primary", Engine: "postgresql", Host: "localhost", Port: 5432, Username: "dev", Database: "app",
	}, SecretUpdate{Password: "hunter2"}, key, userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !c.HasSecret {
		t.Error("HasSecret = false, want true after creating with a password")
	}
	if c.ProjectID != projectID {
		t.Errorf("ProjectID = %q, want %q", c.ProjectID, projectID)
	}

	got, err := Get(context.Background(), pool, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "primary" || got.Engine != "postgresql" {
		t.Errorf("Get = %+v, want name=primary engine=postgresql", got)
	}

	list, err := ListForProject(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("ListForProject = %+v, want exactly [%s]", list, c.ID)
	}

	ok, err := Delete(context.Background(), pool, c.ID)
	if err != nil || !ok {
		t.Fatalf("Delete: ok=%v err=%v", ok, err)
	}
	if _, err := Get(context.Background(), pool, c.ID); err != ErrNotFound {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestUpdate_SecretNilKeepsExistingCredential(t *testing.T) {
	pool := testPool(t)
	projectID, userID := testProject(t, pool)
	key := testKey()

	c, err := Create(context.Background(), pool, projectID, Fields{
		Name: "primary", Engine: "postgresql", Host: "localhost", Port: 5432, Username: "dev", Database: "app",
	}, SecretUpdate{Password: "original-password"}, key, userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update the name only, secret=nil — must not disturb the stored password.
	updated, err := Update(context.Background(), pool, c.ID, Fields{
		Name: "renamed", Engine: c.Engine, Host: c.Host, Port: c.Port, Username: c.Username, Database: c.Database,
	}, nil, key)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "renamed" || !updated.HasSecret {
		t.Errorf("Update result = %+v, want name=renamed has_secret=true", updated)
	}

	params, err := ResolveParams(context.Background(), pool, c.ID, key)
	if err != nil {
		t.Fatalf("ResolveParams: %v", err)
	}
	if params.Password != "original-password" {
		t.Errorf("Password after secret-less update = %q, want unchanged %q", params.Password, "original-password")
	}
}

func TestUpdate_SecretReplacesCredential(t *testing.T) {
	pool := testPool(t)
	projectID, userID := testProject(t, pool)
	key := testKey()

	c, err := Create(context.Background(), pool, projectID, Fields{
		Name: "primary", Engine: "postgresql", Host: "localhost", Port: 5432, Username: "dev", Database: "app",
	}, SecretUpdate{Password: "old"}, key, userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Update(context.Background(), pool, c.ID, Fields{
		Name: c.Name, Engine: c.Engine, Host: c.Host, Port: c.Port, Username: c.Username, Database: c.Database,
	}, &SecretUpdate{Password: "new", ExtraParams: "connect_timeout=5"}, key); err != nil {
		t.Fatalf("Update: %v", err)
	}

	params, err := ResolveParams(context.Background(), pool, c.ID, key)
	if err != nil {
		t.Fatalf("ResolveParams: %v", err)
	}
	if params.Password != "new" || params.ExtraParams != "connect_timeout=5" {
		t.Errorf("ResolveParams after secret update = %+v, want password=new extra_params=connect_timeout=5", params)
	}
}
