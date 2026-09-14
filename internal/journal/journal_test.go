package journal

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
	if _, err := pool.Exec(context.Background(), "TRUNCATE journal_attachments, journal_notes, projects, runtimes, users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	u, err := auth.CreateUser(ctx, pool, "journaler@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtimes (name, token_hash, status, created_by)
		VALUES ('dev01', 'journal-test-hash', 'online', $1)`, u.ID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	var projectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (name, runtime_id, working_directory, shell, environment, created_by)
		VALUES ('Journal Proj', (SELECT id FROM runtimes LIMIT 1), '/tmp', '/bin/bash', '{}', $1)
		RETURNING id`, u.ID,
	).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return projectID
}

func fields(body string) Fields {
	return Fields{BodyHTML: body}
}

func TestNoteLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID := testProject(t, pool)

	n, err := Create(ctx, pool, projectID, fields("<p>first <b>note</b></p>"), "")
	if err == nil {
		t.Fatalf("create with unknown user should fail, got %v", n)
	}

	u, err := auth.CreateUser(ctx, pool, "author@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create author: %v", err)
	}
	n, err = Create(ctx, pool, projectID, fields("<p>first <b>note</b></p>"), u.ID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.BodyHTML != "<p>first <b>note</b></p>" || n.CreatorName != "author@example.com" {
		t.Fatalf("unexpected note: %+v", n)
	}

	got, err := GetForProject(ctx, pool, projectID, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != n.ID {
		t.Fatalf("wrong note returned")
	}
	if _, err := GetForProject(ctx, pool, "00000000-0000-0000-0000-000000000000", n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project get should be ErrNotFound, got %v", err)
	}

	updated, err := Update(ctx, pool, n.ID, Fields{BodyHTML: "<p>edited</p>"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.BodyHTML != "<p>edited</p>" || updated.CreatorName != "author@example.com" {
		t.Fatalf("unexpected updated note: %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Fatalf("updated_at should move past created_at")
	}

	list, total, err := ListForProject(ctx, pool, projectID, 25, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("unexpected list: total=%d len=%d", total, len(list))
	}
	if list[0].Snippet != "edited" {
		t.Fatalf("snippet should be HTML-stripped, got %q", list[0].Snippet)
	}
	if list[0].BodyHTML != "" {
		t.Fatalf("list should not carry full body HTML")
	}

	ok, err := Delete(ctx, pool, n.ID)
	if err != nil || !ok {
		t.Fatalf("delete: %v ok=%v", err, ok)
	}
	if _, err := Get(ctx, pool, n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete should be ErrNotFound, got %v", err)
	}
}

func TestSnippetTruncates(t *testing.T) {
	long := "<p>" + string(make([]byte, 0)) + "abcdefghij"
	for i := 0; i < 20; i++ {
		long += "0123456789"
	}
	s := Snippet(long)
	if runes := []rune(s); len(runes) > 121 {
		t.Fatalf("snippet too long: %d", len(runes))
	}
	if runes := []rune(s); len(runes) > 121 || runes[len(runes)-1] != '…' {
		t.Fatalf("expected ≤120 chars plus ellipsis, got %d runes ending %q", len(runes), runes[len(runes)-1])
	}
	if got := Snippet("<p>  multiple\n\tspaces </p>"); got != "multiple spaces" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}
}

func TestAttachmentLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID := testProject(t, pool)
	u, err := auth.CreateUser(ctx, pool, "author@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create author: %v", err)
	}
	n, err := Create(ctx, pool, projectID, fields("<p>with attachments</p>"), u.ID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	a, err := AddAttachment(ctx, pool, n.ID, "shot.png", "image/png", []byte("pngbytes"))
	if err != nil {
		t.Fatalf("add attachment: %v", err)
	}
	if a.SizeBytes != 8 || a.Filename != "shot.png" || a.Content != nil {
		t.Fatalf("unexpected attachment metadata: %+v", a)
	}

	count, err := CountAttachments(ctx, pool, n.ID)
	if err != nil || count != 1 {
		t.Fatalf("count: %v count=%d", err, count)
	}

	atts, err := ListAttachments(ctx, pool, n.ID)
	if err != nil || len(atts) != 1 || atts[0].Content != nil {
		t.Fatalf("list attachments: %v %+v", err, atts)
	}

	full, err := GetAttachment(ctx, pool, n.ID, a.ID)
	if err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	if string(full.Content) != "pngbytes" {
		t.Fatalf("content mismatch: %q", full.Content)
	}
	if _, err := GetAttachment(ctx, pool, "00000000-0000-0000-0000-000000000000", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-note attachment get should be ErrNotFound, got %v", err)
	}

	// Attachments die with the note (FK CASCADE).
	if _, err := Delete(ctx, pool, n.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	if _, err := GetAttachment(ctx, pool, n.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attachment should be gone with the note, got %v", err)
	}
}

func TestCommitMetadataRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID := testProject(t, pool)
	u, err := auth.CreateUser(ctx, pool, "author@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create author: %v", err)
	}
	when := time.Date(2026, 9, 14, 11, 11, 0, 0, time.UTC)
	n, err := Create(ctx, pool, projectID, Fields{
		BodyHTML: "<p>shipped it</p>", CommitSHA: "abc123", CommitSubject: "fix bug",
		CommitAuthor: "Brian", CommitDatetime: &when,
	}, u.ID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := Get(ctx, pool, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CommitSHA != "abc123" || got.CommitSubject != "fix bug" || got.CommitAuthor != "Brian" ||
		got.CommitDatetime == nil || !got.CommitDatetime.Equal(when) {
		t.Fatalf("commit metadata lost: %+v", got)
	}
}
