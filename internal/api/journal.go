package api

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/journal"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// journalNoteResponse is one note as returned by the API.
type journalNoteResponse struct {
	ID             string                      `json:"id"`
	ProjectID      string                      `json:"project_id"`
	BodyHTML       string                      `json:"body_html"`
	Snippet        string                      `json:"snippet,omitempty"`
	CommitSHA      string                      `json:"commit_sha"`
	CommitSubject  string                      `json:"commit_subject"`
	CommitAuthor   string                      `json:"commit_author"`
	CommitDatetime *time.Time                  `json:"commit_datetime"`
	CreatedBy      string                      `json:"created_by"`
	CreatorName    string                      `json:"creator_name"`
	CreatedAt      time.Time                   `json:"created_at"`
	UpdatedAt      time.Time                   `json:"updated_at"`
	Attachments    []journalAttachmentResponse `json:"attachments,omitempty"`
}

type journalAttachmentResponse struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

func toJournalNoteResponse(n journal.Note, atts []journal.Attachment) journalNoteResponse {
	r := journalNoteResponse{
		ID: n.ID, ProjectID: n.ProjectID, BodyHTML: n.BodyHTML, Snippet: n.Snippet,
		CommitSHA: n.CommitSHA, CommitSubject: n.CommitSubject, CommitAuthor: n.CommitAuthor,
		CommitDatetime: n.CommitDatetime, CreatedBy: n.CreatedBy, CreatorName: n.CreatorName,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
	if atts != nil {
		r.Attachments = make([]journalAttachmentResponse, 0, len(atts))
		for _, a := range atts {
			r.Attachments = append(r.Attachments, toJournalAttachmentResponse(a))
		}
	}
	return r
}

func toJournalAttachmentResponse(a journal.Attachment) journalAttachmentResponse {
	return journalAttachmentResponse{ID: a.ID, Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreatedAt: a.CreatedAt}
}

// decodeJournalNote decodes a note create/update body. Commit metadata is
// free text captured as-is (trimmed); an empty body_html is rejected.
func decodeJournalNote(w http.ResponseWriter, r *http.Request) (journal.Fields, bool) {
	var req struct {
		BodyHTML       string     `json:"body_html"`
		CommitSHA      string     `json:"commit_sha"`
		CommitSubject  string     `json:"commit_subject"`
		CommitAuthor   string     `json:"commit_author"`
		CommitDatetime *time.Time `json:"commit_datetime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return journal.Fields{}, false
	}
	if strings.TrimSpace(req.BodyHTML) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is required"})
		return journal.Fields{}, false
	}
	return journal.Fields{
		BodyHTML:  req.BodyHTML,
		CommitSHA: strings.TrimSpace(req.CommitSHA), CommitSubject: strings.TrimSpace(req.CommitSubject),
		CommitAuthor: strings.TrimSpace(req.CommitAuthor), CommitDatetime: req.CommitDatetime,
	}, true
}

// listJournalNotesHandler GET .../journal — one server-side page.
func listJournalNotesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		limit, offset := 25, 0
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 100 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1-100"})
				return
			}
			limit = n
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be a non-negative integer"})
				return
			}
			offset = n
		}
		notes, total, err := journal.ListForProject(r.Context(), db.Pool, project.ID, limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list journal notes"})
			return
		}
		out := make([]journalNoteResponse, 0, len(notes))
		for _, n := range notes {
			out = append(out, toJournalNoteResponse(n, nil))
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": out, "total": total, "limit": limit, "offset": offset})
	}
}

// createJournalNoteHandler POST .../journal — creates one note.
func createJournalNoteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		f, ok := decodeJournalNote(w, r)
		if !ok {
			return
		}
		n, err := journal.Create(r.Context(), db.Pool, project.ID, f, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create journal note"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventJournalCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "journal_note", n.ID, map[string]any{"project_id": project.ID}, clientIP(r))
		writeJSON(w, http.StatusCreated, toJournalNoteResponse(n, nil))
	}
}

// journalNote resolves {noteID} within the authorized project, writing a
// 404 and returning ok=false when the note doesn't belong to it.
func journalNote(w http.ResponseWriter, r *http.Request, db *database.DB, project projects.Project) (journal.Note, bool) {
	n, err := journal.GetForProject(r.Context(), db.Pool, project.ID, r.PathValue("noteID"))
	if err == journal.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "journal note not found"})
		return journal.Note{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load journal note"})
		return journal.Note{}, false
	}
	return n, true
}

// getJournalNoteHandler GET .../journal/notes/{noteID} — full note with
// its attachment list.
func getJournalNoteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		n, ok := journalNote(w, r, db, project)
		if !ok {
			return
		}
		atts, err := journal.ListAttachments(r.Context(), db.Pool, n.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load attachments"})
			return
		}
		writeJSON(w, http.StatusOK, toJournalNoteResponse(n, atts))
	}
}

// updateJournalNoteHandler PUT .../journal/notes/{noteID} — overwrites
// body and commit metadata; any user with project access may edit.
func updateJournalNoteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		if _, ok := journalNote(w, r, db, project); !ok {
			return
		}
		f, ok := decodeJournalNote(w, r)
		if !ok {
			return
		}
		n, err := journal.Update(r.Context(), db.Pool, r.PathValue("noteID"), f)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update journal note"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventJournalUpdated, audit.Actor{UserID: user.ID, Email: user.Email}, "journal_note", n.ID, map[string]any{"project_id": project.ID}, clientIP(r))
		writeJSON(w, http.StatusOK, toJournalNoteResponse(n, nil))
	}
}

// deleteJournalNoteHandler DELETE .../journal/notes/{noteID} — drops the
// note and its attachments.
func deleteJournalNoteHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		if _, ok := journalNote(w, r, db, project); !ok {
			return
		}
		okDel, err := journal.Delete(r.Context(), db.Pool, r.PathValue("noteID"))
		if err != nil || !okDel {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete journal note"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventJournalDeleted, audit.Actor{UserID: user.ID, Email: user.Email}, "journal_note", r.PathValue("noteID"), map[string]any{"project_id": project.ID}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// uploadJournalAttachmentHandler POST .../journal/notes/{noteID}/attachments
// — multipart form field "file". Enforces 25 MB per file and 10 per note.
func uploadJournalAttachmentHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())
		n, ok := journalNote(w, r, db, project)
		if !ok {
			return
		}
		count, err := journal.CountAttachments(r.Context(), db.Pool, n.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check attachments"})
			return
		}
		if count >= journal.MaxAttachments {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "note already has the maximum number of attachments"})
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart form with a \"file\" field is required"})
			return
		}
		defer file.Close()
		if header.Size > journal.MaxAttachmentBytes {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "attachment exceeds the 25 MB limit"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, journal.MaxAttachmentBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read uploaded file"})
			return
		}
		if len(data) > journal.MaxAttachmentBytes {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "attachment exceeds the 25 MB limit"})
			return
		}

		ctype := header.Header.Get("Content-Type")
		if ctype != "" {
			if _, _, err := mime.ParseMediaType(ctype); err != nil {
				ctype = ""
			}
		}
		a, err := journal.AddAttachment(r.Context(), db.Pool, n.ID, downloadName(header.Filename), ctype, data)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store attachment"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventJournalAttachmentAdded, audit.Actor{UserID: user.ID, Email: user.Email}, "journal_note", n.ID, map[string]any{"project_id": project.ID, "attachment_id": a.ID, "filename": a.Filename}, clientIP(r))
		writeJSON(w, http.StatusCreated, toJournalAttachmentResponse(a))
	}
}

// downloadJournalAttachmentHandler GET .../journal/notes/{noteID}/attachments/{attachmentID}
// — always serves as a download (safe disposition), like the files API;
// <img> previews fetch the bytes and ignore the disposition.
func downloadJournalAttachmentHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		n, ok := journalNote(w, r, db, project)
		if !ok {
			return
		}
		a, err := journal.GetAttachment(r.Context(), db.Pool, n.ID, r.PathValue("attachmentID"))
		if err == journal.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load attachment"})
			return
		}
		ctype := a.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+downloadName(a.Filename)+"\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(a.Content)
	}
}

// gitLogHandler GET .../git/log?limit= — recent commits for the commit
// picker.
func gitLogHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 100 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1-100"})
				return
			}
			limit = n
		}
		result, ok := gitCommand(w, r, conn, protocol.GitCommandPayload{Action: "log", Root: root, Limit: limit})
		if !ok {
			return
		}
		if result.Commits == nil {
			result.Commits = []protocol.GitCommitInfo{}
		}
		writeJSON(w, http.StatusOK, result.Commits)
	}
}
