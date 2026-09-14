// Package journal stores Project Journal notes: rich-text entries
// (Summernote HTML, sanitized at render time — see
// docs/JOURNAL_REQUIREMENTS.md) with an optional git-commit association
// and attachments stored Control-Server-side.
package journal

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// Limits enforced by the API layer on top of these storage primitives.
const (
	MaxAttachmentBytes = 25 << 20 // 25 MB per attachment
	MaxAttachments     = 10
)

// Note is one stored journal entry. The commit fields are captured
// metadata (SHA, subject, author, datetime) recorded when the note was
// linked — not a live reference.
type Note struct {
	ID             string
	ProjectID      string
	BodyHTML       string
	CommitSHA      string
	CommitSubject  string
	CommitAuthor   string
	CommitDatetime *time.Time
	CreatedBy      string
	CreatorName    string // users.display_name, falling back to email
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Snippet is the first ~120 characters of the HTML-stripped body,
	// populated by ListForProject only.
	Snippet string
}

// Fields groups the user-supplied note properties, used for create and
// update.
type Fields struct {
	BodyHTML       string
	CommitSHA      string
	CommitSubject  string
	CommitAuthor   string
	CommitDatetime *time.Time
}

// Attachment describes one stored attachment; Content is only populated
// by GetAttachment.
type Attachment struct {
	ID          string
	NoteID      string
	Filename    string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
	Content     []byte
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)
var whitespace = regexp.MustCompile(`\s+`)

// Snippet strips tags from an HTML body and truncates to ~120
// characters. ponytail: regex strip, enough for a table preview column;
// full rendering is DOMPurify's job, not this.
func Snippet(body string) string {
	text := strings.TrimSpace(whitespace.ReplaceAllString(htmlTag.ReplaceAllString(body, " "), " "))
	runes := []rune(text)
	if len(runes) > 120 {
		runes = append(runes[:120], '…')
	}
	return string(runes)
}

// creatorNameExpr renders display_name with an email fallback; u is the
// users table joined on created_by (or the subquery form for UPDATE's
// RETURNING, which can't reference a join).
const creatorNameExpr = `COALESCE(NULLIF(display_name, ''), email)`

// noteCore is the plain journal_notes column list (table aliased n).
// CreatorName is appended per call site: a users join for SELECTs, or a
// scalar subquery for INSERT/UPDATE RETURNING.
const noteCore = `n.id, n.project_id, n.body_html, n.commit_sha, n.commit_subject,
	n.commit_author, n.commit_datetime, n.created_by, n.created_at, n.updated_at`

const noteColumns = noteCore + `,
	` + creatorNameExpr

func scanNote(row pgx.Row) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.ProjectID, &n.BodyHTML, &n.CommitSHA, &n.CommitSubject,
		&n.CommitAuthor, &n.CommitDatetime, &n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.CreatorName)
	return n, err
}

// creatorSubquery produces the creator-name column for an
// INSERT/UPDATE ... RETURNING; ref is the created_by reference visible
// there ("n.created_by" under the n alias).
func creatorSubquery(ref string) string {
	return `(SELECT ` + creatorNameExpr + ` FROM users WHERE id = ` + ref + `)`
}

// Create inserts a new note on projectID, owned by createdByUserID.
func Create(ctx context.Context, pool *pgxpool.Pool, projectID string, f Fields, createdByUserID string) (Note, error) {
	n, err := scanNote(pool.QueryRow(ctx, `
		INSERT INTO journal_notes AS n (project_id, body_html, commit_sha, commit_subject, commit_author, commit_datetime, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+noteCore+`, `+creatorSubquery("n.created_by"),
		projectID, f.BodyHTML, f.CommitSHA, f.CommitSubject, f.CommitAuthor, f.CommitDatetime, createdByUserID,
	))
	if err != nil {
		return Note{}, err
	}
	return n, nil
}

// ListForProject returns a page of notes on projectID, newest first,
// alongside the total matching count for pagination. Snippet is
// populated on each note; BodyHTML is left empty (the list view never
// renders full bodies — the detail modal fetches them individually).
func ListForProject(ctx context.Context, pool *pgxpool.Pool, projectID string, limit, offset int) ([]Note, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_notes WHERE project_id = $1`, projectID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := pool.Query(ctx, `SELECT `+noteColumns+`
		FROM journal_notes n JOIN users u ON u.id = n.created_by
		WHERE n.project_id = $1 ORDER BY n.created_at DESC LIMIT $2 OFFSET $3`,
		projectID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, 0, err
		}
		n.Snippet = Snippet(n.BodyHTML)
		n.BodyHTML = ""
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// Get looks up a note by ID regardless of project. Callers are
// responsible for authorizing access to the result.
func Get(ctx context.Context, pool *pgxpool.Pool, id string) (Note, error) {
	n, err := scanNote(pool.QueryRow(ctx, `SELECT `+noteColumns+`
		FROM journal_notes n JOIN users u ON u.id = n.created_by WHERE n.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}
	return n, nil
}

// GetForProject looks up a note and verifies it belongs to projectID.
func GetForProject(ctx context.Context, pool *pgxpool.Pool, projectID, id string) (Note, error) {
	n, err := Get(ctx, pool, id)
	if err != nil {
		return Note{}, err
	}
	if n.ProjectID != projectID {
		return Note{}, ErrNotFound
	}
	return n, nil
}

// Update overwrites a note's fields and updated_at, preserving the
// original creator, and reports whether a matching row was found.
func Update(ctx context.Context, pool *pgxpool.Pool, id string, f Fields) (Note, error) {
	n, err := scanNote(pool.QueryRow(ctx, `
		UPDATE journal_notes AS n SET
			body_html = $2, commit_sha = $3, commit_subject = $4, commit_author = $5,
			commit_datetime = $6, updated_at = now()
		WHERE n.id = $1
		RETURNING `+noteCore+`, `+creatorSubquery("n.created_by"),
		id, f.BodyHTML, f.CommitSHA, f.CommitSubject, f.CommitAuthor, f.CommitDatetime,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}
	return n, nil
}

// Delete removes a note and its attachments (FK CASCADE) and reports
// whether a matching row was found.
func Delete(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM journal_notes WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// AddAttachment stores one attachment on noteID. The caller enforces the
// per-note count limit (MaxAttachments) — the check is a simple SELECT
// count performed by the caller alongside its other validation.
func AddAttachment(ctx context.Context, pool *pgxpool.Pool, noteID, filename, contentType string, content []byte) (Attachment, error) {
	var a Attachment
	err := pool.QueryRow(ctx, `
		INSERT INTO journal_attachments (note_id, filename, content_type, size_bytes, content)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, note_id, filename, content_type, size_bytes, created_at`,
		noteID, filename, contentType, len(content), content,
	).Scan(&a.ID, &a.NoteID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt)
	if err != nil {
		return Attachment{}, err
	}
	return a, nil
}

// ListAttachments returns a note's attachments, without content.
func ListAttachments(ctx context.Context, pool *pgxpool.Pool, noteID string) ([]Attachment, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, note_id, filename, content_type, size_bytes, created_at
		FROM journal_attachments WHERE note_id = $1 ORDER BY created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.NoteID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAttachments returns how many attachments noteID has.
func CountAttachments(ctx context.Context, pool *pgxpool.Pool, noteID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_attachments WHERE note_id = $1`, noteID).Scan(&n)
	return n, err
}

// GetAttachment looks up one attachment with its content, verifying it
// belongs to noteID.
func GetAttachment(ctx context.Context, pool *pgxpool.Pool, noteID, id string) (Attachment, error) {
	var a Attachment
	err := pool.QueryRow(ctx, `
		SELECT id, note_id, filename, content_type, size_bytes, created_at, content
		FROM journal_attachments WHERE id = $1 AND note_id = $2`, id, noteID,
	).Scan(&a.ID, &a.NoteID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt, &a.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, err
	}
	return a, nil
}
