package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/database"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// idleReadTimeout bounds how long the server waits for a browser to send
// anything (input bytes) before treating the stream as dead. Terminal
// sessions are often idle for long stretches (reading output, not typing),
// so this is generous compared to the runtime heartbeat timeout.
const idleReadTimeout = 5 * time.Minute

// browserConn adapts a *websocket.Conn to sessions.BrowserConn, serializing
// writes the same way wsConn does on the runtime side.
type browserConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (b *browserConn) WriteOutput(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn.Write(ctx, websocket.MessageBinary, data)
}

func (b *browserConn) Close() {
	_ = b.conn.Close(websocket.StatusNormalClosure, "detached")
}

// termSessionStreamHandler serves the per-session binary terminal stream
// (WSS /api/v1/sessions/{sessionID}/stream). Opening it attaches the
// browser as the session's live viewer; closing it detaches (the PTY
// itself is unaffected — see PROTOCOL.md session lifecycle).
func termSessionStreamHandler(db *database.DB, registry *runtimes.Registry, hub *sessions.Hub, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, project, ok := authorizedTermSession(w, r, db, r.PathValue("sessionID"))
		if !ok {
			return
		}
		if s.Status == sessions.StatusExited || s.Status == sessions.StatusFailed || s.Status == sessions.StatusClosed {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session is not running"})
			return
		}
		runtimeConn, online := registry.Get(project.RuntimeID)
		if !online {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1 << 20)

		bc := &browserConn{conn: conn}
		hub.Attach(s.ID, bc)
		if err := sessions.MarkRunning(r.Context(), db.Pool, s.ID); err != nil {
			logger.Error("session mark running failed", "session_id", s.ID, "error", err)
		}
		defer func() {
			hub.Detach(s.ID, bc)
			if err := sessions.MarkDetached(context.Background(), db.Pool, s.ID); err != nil {
				logger.Error("session mark detached failed", "session_id", s.ID, "error", err)
			}
		}()

		for {
			readCtx, cancel := context.WithTimeout(r.Context(), idleReadTimeout)
			msgType, data, err := conn.Read(readCtx)
			cancel()
			if err != nil {
				return
			}
			if msgType != websocket.MessageBinary {
				continue
			}
			if err := runtimeConn.SendFrame(s.ID, data); err != nil {
				logger.Warn("forward input to runtime failed", "session_id", s.ID, "error", err)
				return
			}
		}
	}
}
