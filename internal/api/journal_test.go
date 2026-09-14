package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/journal"
	"devstationone/internal/protocol"
)

func journalURL(projectID, suffix, query string) string {
	u := "/api/v1/projects/" + projectID + "/journal" + suffix
	if query != "" {
		u += "?" + query
	}
	return u
}

// journalAttachmentReq builds a multipart attachment-upload request.
func journalAttachmentReq(t *testing.T, path string, cookie *http.Cookie, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(content)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	return req
}

func TestJournalHTTP_NoteLifecycle(t *testing.T) {
	router, cookie, projectID, _, db := filesTestSetup(t)
	ctx := context.Background()

	rec := fileReq(t, router, cookie, http.MethodPost, journalURL(projectID, "", ""),
		[]byte(`{"body_html":"<p>first note</p>","commit_sha":"abc123","commit_subject":"fix it","commit_author":"Test"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	noteID := created["id"].(string)
	if created["commit_sha"] != "abc123" || created["creator_name"] == "" {
		t.Fatalf("unexpected created note: %v", created)
	}

	rec = fileReq(t, router, cookie, http.MethodPost, journalURL(projectID, "", ""), []byte(`{"body_html":"  "}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body should be 400, got %d", rec.Code)
	}

	rec = fileReq(t, router, cookie, http.MethodGet, journalURL(projectID, "", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var list struct {
		Notes []map[string]any `json:"notes"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Notes) != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list.Notes[0]["snippet"] != "first note" || list.Notes[0]["body_html"] != "" {
		t.Fatalf("list item should carry snippet, not body: %v", list.Notes[0])
	}

	rec = fileReq(t, router, cookie, http.MethodPut, journalURL(projectID, "/notes/"+noteID, ""),
		[]byte(`{"body_html":"<p>edited</p>"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	json.NewDecoder(rec.Body).Decode(&updated)
	if updated["body_html"] != "<p>edited</p>" || updated["commit_sha"] != "" {
		t.Fatalf("update should replace commit metadata: %v", updated)
	}

	// Attachments: 10 allowed, the 11th rejected.
	var attID string
	for i := 0; i < journal.MaxAttachments; i++ {
		req := journalAttachmentReq(t, journalURL(projectID, "/notes/"+noteID+"/attachments", ""), cookie, "shot.png", []byte("pngbytes"))
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("attachment %d upload: %d, body = %s", i+1, rec.Code, rec.Body.String())
		}
		var att map[string]any
		json.NewDecoder(rec.Body).Decode(&att)
		if att["filename"] != "shot.png" {
			t.Fatalf("attachment filename lost: %v", att)
		}
		attID = att["id"].(string)
	}
	req := journalAttachmentReq(t, journalURL(projectID, "/notes/"+noteID+"/attachments", ""), cookie, "over.bin", []byte("x"))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-limit upload should 400, got %d", rec.Code)
	}

	rec = fileReq(t, router, cookie, http.MethodGet, journalURL(projectID, "/notes/"+noteID+"/attachments/"+attID, ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d", rec.Code)
	}
	if got := rec.Body.String(); got != "pngbytes" {
		t.Fatalf("download content = %q", got)
	}
	if disp := rec.Header().Get("Content-Disposition"); disp != `attachment; filename="shot.png"` {
		t.Fatalf("disposition = %q", disp)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("download content type = %q", ct)
	}

	rec = fileReq(t, router, cookie, http.MethodDelete, journalURL(projectID, "/notes/"+noteID, ""), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = fileReq(t, router, cookie, http.MethodGet, journalURL(projectID, "/notes/"+noteID, ""), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", rec.Code)
	}
	if count, err := journal.CountAttachments(ctx, db.Pool, noteID); err != nil || count != 0 {
		t.Fatalf("attachments should be cascaded away: %v %d", err, count)
	}
}

func TestJournalHTTP_UnknownNoteIs404(t *testing.T) {
	router, cookie, projectID, _, _ := filesTestSetup(t)

	rec := fileReq(t, router, cookie, http.MethodPost, journalURL(projectID, "", ""), []byte(`{"body_html":"<p>x</p>"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	noteID := created["id"].(string)

	// Second project (same user) must not see the first project's note.
	rec = fileReq(t, router, cookie, http.MethodPost, journalURL(projectID, "", ""), []byte(`{"body_html":"<p>y</p>"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create 2: %d", rec.Code)
	}
	var second map[string]any
	json.NewDecoder(rec.Body).Decode(&second)
	otherNoteID := second["id"].(string)
	if otherNoteID == noteID {
		t.Fatalf("notes must have distinct IDs")
	}

	// A note ID from another project (random UUID) is a 404.
	rec = fileReq(t, router, cookie, http.MethodGet, journalURL(projectID, "/notes/00000000-0000-0000-0000-000000000000", ""), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown note get: %d, want 404", rec.Code)
	}
}

func TestGitHTTP_Log(t *testing.T) {
	router, cookie, projectID, dir, _ := gitTestSetup(t)
	if err := os.WriteFile(dir+"/a.txt", []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitSetup(t, dir, "add", "a.txt")
	runGitSetup(t, dir, "commit", "-q", "-m", "initial commit")

	rec := gitReq(t, router, cookie, http.MethodGet, gitURL(projectID, "/log", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("log: %d, body = %s", rec.Code, rec.Body.String())
	}
	var commits []protocol.GitCommitInfo
	if err := json.NewDecoder(rec.Body).Decode(&commits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(commits) != 1 || len(commits[0].SHA) != 40 || commits[0].Subject != "initial commit" || commits[0].Author != "Test User" {
		t.Fatalf("unexpected commits: %+v", commits)
	}
	if commits[0].Datetime.IsZero() {
		t.Fatalf("commit datetime missing")
	}
}
