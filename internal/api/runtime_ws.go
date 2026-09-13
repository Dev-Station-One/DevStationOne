package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"devstation/internal/auth"
	"devstation/internal/database"
	"devstation/internal/protocol"
	"devstation/internal/runtimes"
)

// heartbeatTimeout is how long the server waits for any message (heartbeat
// or otherwise) before treating a runtime connection as dead.
const heartbeatTimeout = 45 * time.Second

// runtimeConnectHandler accepts the outbound WebSocket connection a
// Development Runtime opens using its enrollment token as a bearer
// credential.
func runtimeConnectHandler(db *database.DB, registry *runtimes.Registry, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
			return
		}

		rt, err := runtimes.GetByTokenHash(r.Context(), db.Pool, auth.HashToken(token))
		if errors.Is(err, runtimes.ErrNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown runtime token"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "runtime lookup failed"})
			return
		}
		if rt.Status == runtimes.StatusRevoked {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "runtime revoked"})
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}

		handleRuntimeConnection(r.Context(), conn, db, registry, logger, rt)
	}
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

// wsConn adapts a *websocket.Conn to runtimes.Conn.
type wsConn struct {
	c *websocket.Conn
}

func (w wsConn) Revoke() error {
	env := protocol.Envelope{Version: protocol.Version, Type: protocol.MessageRevoked}
	data, _ := json.Marshal(env)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.c.Write(ctx, websocket.MessageText, data)
	return w.c.Close(websocket.StatusNormalClosure, "revoked")
}

func handleRuntimeConnection(ctx context.Context, conn *websocket.Conn, db *database.DB, registry *runtimes.Registry, logger *slog.Logger, rt runtimes.Runtime) {
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	helloCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	_, data, err := conn.Read(helloCtx)
	cancel()
	if err != nil {
		logger.Warn("runtime connect: read hello failed", "runtime_id", rt.ID, "error", err)
		return
	}

	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != protocol.MessageHello {
		logger.Warn("runtime connect: expected hello message", "runtime_id", rt.ID)
		conn.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	var hello protocol.HelloPayload
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		logger.Warn("runtime connect: invalid hello payload", "runtime_id", rt.ID, "error", err)
		conn.Close(websocket.StatusPolicyViolation, "invalid hello payload")
		return
	}

	if err := runtimes.MarkOnline(ctx, db.Pool, rt.ID, hello); err != nil {
		logger.Error("runtime connect: mark online failed", "runtime_id", rt.ID, "error", err)
		return
	}

	wc := wsConn{c: conn}
	registry.Register(rt.ID, wc)
	defer registry.Unregister(rt.ID, wc)
	defer func() {
		if err := runtimes.MarkOffline(context.Background(), db.Pool, rt.ID); err != nil {
			logger.Error("runtime disconnect: mark offline failed", "runtime_id", rt.ID, "error", err)
		}
	}()

	logger.Info("runtime connected", "runtime_id", rt.ID, "name", rt.Name, "hostname", hello.Hostname)

	for {
		readCtx, cancelRead := context.WithTimeout(ctx, heartbeatTimeout)
		_, data, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			logger.Info("runtime disconnected", "runtime_id", rt.ID, "error", err)
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if env.Type == protocol.MessageHeartbeat {
			if err := runtimes.Heartbeat(ctx, db.Pool, rt.ID); err != nil {
				logger.Error("runtime heartbeat update failed", "runtime_id", rt.ID, "error", err)
			}
		}
	}
}
