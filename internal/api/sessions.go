package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// openTimeout bounds how long a session-create request waits for the
// runtime to confirm a PTY actually started.
const openTimeout = 10 * time.Second

type termSessionRequest struct {
	Name       string `json:"name"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	RenderMode string `json:"render_mode,omitempty"`
}

type termSessionResponse struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Cols       int       `json:"cols"`
	Rows       int       `json:"rows"`
	ExitCode   *int      `json:"exit_code"`
	Error      string    `json:"error,omitempty"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	RenderMode string    `json:"render_mode"`
}

func toTermSessionResponse(s sessions.Session) termSessionResponse {
	return termSessionResponse{
		ID:         s.ID,
		ProjectID:  s.ProjectID,
		Name:       s.Name,
		Status:     s.Status,
		Cols:       s.Cols,
		Rows:       s.Rows,
		ExitCode:   s.ExitCode,
		Error:      s.ErrorMessage,
		CreatedBy:  s.CreatedBy,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
		RenderMode: s.RenderMode,
	}
}

// authorizedTermSession looks up a session and its Project, confirming the
// authenticated user may access it. It writes a 404 (never 403, matching
// authorizedProject) if not.
func authorizedTermSession(w http.ResponseWriter, r *http.Request, db *database.DB, sessionID string) (sessions.Session, projects.Project, bool) {
	s, err := sessions.Get(r.Context(), db.Pool, sessionID)
	if errors.Is(err, sessions.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return sessions.Session{}, projects.Project{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up session"})
		return sessions.Session{}, projects.Project{}, false
	}

	p, ok := authorizedProject(w, r, db, s.ProjectID)
	if !ok {
		// authorizedProject already wrote a 404 (or 401/500).
		return sessions.Session{}, projects.Project{}, false
	}
	return s, p, true
}

func listTermSessionsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		list, err := sessions.ListForProject(r.Context(), db.Pool, project.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list sessions"})
			return
		}
		resp := make([]termSessionResponse, 0, len(list))
		for _, s := range list {
			resp = append(resp, toTermSessionResponse(s))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createTermSessionHandler(db *database.DB, registry *runtimes.Registry, hub *sessions.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}

		var req termSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if req.Cols <= 0 {
			req.Cols = 80
		}
		if req.Rows <= 0 {
			req.Rows = 24
		}
		if req.RenderMode == "" {
			req.RenderMode = "xterm"
		}
		if req.RenderMode != "xterm" && req.RenderMode != "cards" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "render_mode must be 'xterm' or 'cards'"})
			return
		}

		user, _ := userFromContext(r.Context())
		ctx := r.Context()

		sess, err := sessions.Create(ctx, db.Pool, project.ID, req.Name, req.Cols, req.Rows, user.ID, req.RenderMode)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
			return
		}

		conn, online := registry.Get(project.RuntimeID)
		if !online {
			_ = sessions.MarkFailed(ctx, db.Pool, sess.ID, "runtime offline")
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}

		// The OS user is not stored on the Project — a runtime always runs
		// as one fixed user (ADR-014), reported live in its capabilities.
		rt, err := runtimes.GetByID(ctx, db.Pool, project.RuntimeID)
		if err != nil {
			_ = sessions.MarkFailed(ctx, db.Pool, sess.ID, "failed to look up runtime")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up runtime"})
			return
		}

		if err := sessions.MarkStarting(ctx, db.Pool, sess.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update session"})
			return
		}

		waitCh := hub.AwaitOpen(sess.ID)
		openErr := conn.SendEnvelope(protocol.MessageSessionOpen, protocol.SessionOpenPayload{
			SessionID: sess.ID,
			Shell:     project.Shell,
			Dir:       project.WorkingDirectory,
			OSUser:    rt.Capabilities.OSUsername,
			Env:       project.Environment,
			Cols:      uint16(req.Cols),
			Rows:      uint16(req.Rows),
		})
		if openErr != nil {
			hub.ResolveOpen(sess.ID, nil)
			_ = sessions.MarkFailed(ctx, db.Pool, sess.ID, "failed to dispatch session.open")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach runtime"})
			return
		}

		select {
		case err := <-waitCh:
			if err != nil {
				_ = sessions.MarkFailed(ctx, db.Pool, sess.ID, err.Error())
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "runtime failed to start session: " + err.Error()})
				return
			}
		case <-time.After(openTimeout):
			_ = sessions.MarkFailed(ctx, db.Pool, sess.ID, "timed out waiting for runtime")
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
			return
		}

		final, err := sessions.Get(ctx, db.Pool, sess.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up session"})
			return
		}
		audit.Record(ctx, db.Pool, audit.EventSessionCreated, audit.Actor{UserID: user.ID, Email: user.Email}, "session", final.ID, map[string]any{"project_id": project.ID, "name": final.Name}, clientIP(r))
		writeJSON(w, http.StatusCreated, toTermSessionResponse(final))
	}
}

func getTermSessionHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, _, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toTermSessionResponse(s))
	}
}

func patchTermSessionHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, _, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		var req struct {
			Name *string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		s, err := sessions.Rename(r.Context(), db.Pool, existing.ID, *req.Name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to rename session"})
			return
		}
		writeJSON(w, http.StatusOK, toTermSessionResponse(s))
	}
}

func deleteTermSessionHandler(db *database.DB, registry *runtimes.Registry, hub *sessions.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, project, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		if conn, online := registry.Get(project.RuntimeID); online {
			_ = conn.SendEnvelope(protocol.MessageSessionClose, protocol.SessionClosePayload{SessionID: existing.ID})
		}
		hub.Discard(existing.ID)
		if err := sessions.MarkClosed(r.Context(), db.Pool, existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to close session"})
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventSessionTerminated, audit.Actor{UserID: user.ID, Email: user.Email}, "session", existing.ID, map[string]any{"project_id": project.ID, "name": existing.Name}, clientIP(r))
		w.WriteHeader(http.StatusNoContent)
	}
}

// resizeTermSessionHandler and signalTermSessionHandler require the runtime to be
// online: there is no persisted command queue, matching the rest of the
// control-plane protocol (ADR-012's single in-memory registry).
func resizeTermSessionHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, project, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		var req struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Cols <= 0 || req.Rows <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cols and rows must be positive integers"})
			return
		}
		conn, online := registry.Get(project.RuntimeID)
		if !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}
		if err := conn.SendEnvelope(protocol.MessageSessionResize, protocol.SessionResizePayload{
			SessionID: existing.ID, Cols: uint16(req.Cols), Rows: uint16(req.Rows),
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach runtime"})
			return
		}
		if err := sessions.UpdateSize(r.Context(), db.Pool, existing.ID, req.Cols, req.Rows); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist size"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

var allowedSignals = map[string]bool{
	"SIGINT": true, "SIGTERM": true, "SIGKILL": true, "SIGHUP": true,
	"SIGQUIT": true, "SIGTSTP": true, "SIGCONT": true,
}

func signalTermSessionHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, project, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		var req struct {
			Signal string `json:"signal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !allowedSignals[req.Signal] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported signal"})
			return
		}
		conn, online := registry.Get(project.RuntimeID)
		if !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}
		if err := conn.SendEnvelope(protocol.MessageSessionSignal, protocol.SessionSignalPayload{
			SessionID: existing.ID, Signal: req.Signal,
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach runtime"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// attachTermSessionHandler confirms a session can be attached to (it exists,
// is owned by the caller, and its process is alive) without itself
// streaming bytes — that happens over the WSS stream endpoint, which
// performs the actual attach/detach bookkeeping as the browser connection
// opens and closes.
func attachTermSessionHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, _, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		if s.Status == sessions.StatusExited || s.Status == sessions.StatusFailed || s.Status == sessions.StatusClosed {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session is not running"})
			return
		}
		writeJSON(w, http.StatusOK, toTermSessionResponse(s))
	}
}

// detachTermSessionHandler forces the current viewer (if any) off a session
// without closing it, e.g. so another device can take over.
func detachTermSessionHandler(db *database.DB, hub *sessions.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, _, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		hub.ForceDetach(existing.ID)
		if err := sessions.MarkDetached(r.Context(), db.Pool, existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to detach session"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
