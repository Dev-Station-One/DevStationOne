package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"devstationone/internal/database"
	"devstationone/internal/editor"
)

// editorStateMaxBytes caps one stored editor tab-state document. The shape
// (open tabs + cursor offsets) is small by construction; this is a tamper
// bound, not a feature limit.
const editorStateMaxBytes = 256 << 10

// getEditorStateHandler returns the signed-in user's stored editor tab
// state for the project, or an empty object when nothing is stored yet.
func getEditorStateHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		if _, ok := authorizedProject(w, r, db, r.PathValue("projectID")); !ok {
			return
		}
		state, err := editor.GetState(r.Context(), db.Pool, user.ID, r.PathValue("projectID"))
		if errors.Is(err, editor.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"state": map[string]any{}})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "editor state lookup failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]json.RawMessage{"state": state})
	}
}

// putEditorStateHandler stores the signed-in user's editor tab state for
// the project. The state is an opaque JSON object — the server never
// inspects its contents.
func putEditorStateHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		if _, ok := authorizedProject(w, r, db, r.PathValue("projectID")); !ok {
			return
		}
		projectID := r.PathValue("projectID")

		body := http.MaxBytesReader(w, r.Body, editorStateMaxBytes)
		data, err := io.ReadAll(body)
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "editor state too large"})
			return
		}
		var req struct {
			State json.RawMessage `json:"state"`
		}
		if json.Unmarshal(data, &req) != nil || !json.Valid(req.State) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid editor state"})
			return
		}
		if err := editor.PutState(r.Context(), db.Pool, user.ID, projectID, req.State); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "editor state save failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
