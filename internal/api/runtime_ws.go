package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/auth"
	"devstationone/internal/commandruns"
	"devstationone/internal/database"
	"devstationone/internal/editor"
	"devstationone/internal/events"
	"devstationone/internal/notifications"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// heartbeatTimeout is how long the server waits for any message (heartbeat
// or otherwise) before treating a runtime connection as dead.
const heartbeatTimeout = 45 * time.Second

// runtimeConnectHandler accepts the outbound WebSocket connection a
// Development Runtime opens using its enrollment token as a bearer
// credential.
func runtimeConnectHandler(db *database.DB, registry *runtimes.Registry, hub *sessions.Hub, bus *events.Bus, editorHub *editor.Hub, logsHub *dockerLogsHub, logger *slog.Logger) http.HandlerFunc {
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

		conn, err := websocket.Accept(w, r, wsAcceptOptions)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}

		handleRuntimeConnection(r.Context(), conn, db, registry, hub, bus, editorHub, logsHub, logger, rt)
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

// wsConn adapts a *websocket.Conn to runtimes.Conn. Writes are serialized
// through mu: heartbeats, revocation, and per-session control/output frames
// can all originate from different goroutines (the connection's own read
// loop vs. HTTP handlers acting on a session), and coder/websocket requires
// callers to synchronize concurrent writes themselves.
type wsConn struct {
	mu sync.Mutex
	c  *websocket.Conn

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse
}

// rpcResponse is what a pending SendCommand call is waiting for: either a
// successful payload or an error, never both.
type rpcResponse struct {
	payload json.RawMessage
	err     error
}

func (w *wsConn) write(msgType websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.c.Write(ctx, msgType, data)
}

func (w *wsConn) Revoke() error {
	env := protocol.Envelope{Version: protocol.Version, Type: protocol.MessageRevoked}
	data, _ := json.Marshal(env)
	_ = w.write(websocket.MessageText, data)
	return w.c.Close(websocket.StatusNormalClosure, "revoked")
}

// SendEnvelope sends a JSON control message (session.open, session.resize,
// session.signal, session.close, ...) to the runtime.
func (w *wsConn) SendEnvelope(msgType protocol.MessageType, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: msgType, Payload: body})
	if err != nil {
		return err
	}
	return w.write(websocket.MessageText, data)
}

// SendFrame sends raw terminal input bytes for sessionID to the runtime, as
// a binary-safe frame (see protocol.EncodeFrame).
func (w *wsConn) SendFrame(sessionID string, data []byte) error {
	return w.write(websocket.MessageBinary, protocol.EncodeFrame(sessionID, data))
}

// SendCommand sends msgType with a fresh correlation ID and blocks until a
// matching response arrives (routed by tryResolve from the read loop) or ctx
// is done. It is the generic request/response half of the protocol,
// distinct from SendEnvelope's fire-and-forget messages.
func (w *wsConn) SendCommand(ctx context.Context, msgType protocol.MessageType, payload any) (json.RawMessage, error) {
	id, err := auth.NewSessionToken()
	if err != nil {
		return nil, err
	}

	ch := make(chan rpcResponse, 1)
	w.pendingMu.Lock()
	if w.pending == nil {
		w.pending = make(map[string]chan rpcResponse)
	}
	w.pending[id] = ch
	w.pendingMu.Unlock()
	defer func() {
		w.pendingMu.Lock()
		delete(w.pending, id)
		w.pendingMu.Unlock()
	}()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: msgType, ID: id, Payload: body})
	if err != nil {
		return nil, err
	}
	if err := w.write(websocket.MessageText, data); err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		return res.payload, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tryResolve delivers env to whichever SendCommand call is waiting on its
// correlation ID, reporting whether one was found. Envelope types that
// don't use correlation (e.g. session.ready, which correlates on its
// payload's session_id instead) always have an empty ID and so never match.
func (w *wsConn) tryResolve(env protocol.Envelope) bool {
	if env.ID == "" {
		return false
	}
	w.pendingMu.Lock()
	ch, ok := w.pending[env.ID]
	w.pendingMu.Unlock()
	if !ok {
		return false
	}
	// Every RPC error type carries the same {"error": ...} shape; see
	// ServiceErrorPayload's reuse by file.error/db.error/git.error.
	if env.Type == protocol.MessageServiceError || env.Type == protocol.MessageFileError || env.Type == protocol.MessageDBError || env.Type == protocol.MessageGitError || env.Type == protocol.MessageCodeError || env.Type == protocol.MessageDockerError {
		var e protocol.ServiceErrorPayload
		_ = json.Unmarshal(env.Payload, &e)
		ch <- rpcResponse{err: errors.New(e.Error)}
	} else {
		ch <- rpcResponse{payload: env.Payload}
	}
	return true
}

func handleRuntimeConnection(ctx context.Context, conn *websocket.Conn, db *database.DB, registry *runtimes.Registry, hub *sessions.Hub, bus *events.Bus, editorHub *editor.Hub, dockerLogsHub *dockerLogsHub, logger *slog.Logger, rt runtimes.Runtime) {
	defer conn.CloseNow()
	// The control connection carries the largest legal envelopes, including
	// file.command/file.result payloads transferring up to
	// protocol.MaxFileBytes (base64-encoded). Binary terminal frames are far
	// smaller, so the raised limit affects only the RPC path.
	conn.SetReadLimit(protocol.MaxMessageBytes)

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

	wc := &wsConn{c: conn}
	registry.Register(rt.ID, wc)
	publishRuntimeStatus(bus, rt.ID, true)
	defer registry.Unregister(rt.ID, wc)
	defer publishRuntimeStatus(bus, rt.ID, false)
	defer func() {
		if err := runtimes.MarkOffline(context.Background(), db.Pool, rt.ID); err != nil {
			logger.Error("runtime disconnect: mark offline failed", "runtime_id", rt.ID, "error", err)
		}
	}()

	logger.Info("runtime connected", "runtime_id", rt.ID, "name", rt.Name, "hostname", hello.Hostname)

	// Control-plane envelopes are handled off the read loop (ADR-050): the
	// exit path alone runs four sequential DB round trips, and a slow
	// commit inside the read loop would stall terminal-frame forwarding
	// (hub.Forward) for every session of this runtime. One worker per
	// connection keeps per-session envelope ordering; binary frames stay on
	// the read loop (they only touch the hub's in-memory fast path).
	// owned caches BelongsToRuntime results for binary frames, which arrive
	// far too often for a lookup each. A session's runtime never changes.
	// Only this read loop touches it.
	owned := make(map[string]bool)
	envCh := make(chan protocol.Envelope, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for env := range envCh {
			handleRuntimeEnvelope(context.Background(), env, db, registry, hub, bus, editorHub, dockerLogsHub, logger, rt.ID)
		}
	}()

	for {
		readCtx, cancelRead := context.WithTimeout(ctx, heartbeatTimeout)
		msgType, data, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			logger.Info("runtime disconnected", "runtime_id", rt.ID, "error", err)
			break
		}

		if msgType == websocket.MessageBinary {
			if sessionID, payload, ok := protocol.DecodeFrame(data); ok {
				own, seen := owned[sessionID]
				if !seen {
					own = sessions.BelongsToRuntime(ctx, db.Pool, sessionID, rt.ID)
					if len(owned) >= 4096 {
						clear(owned)
					}
					owned[sessionID] = own
				}
				if own {
					hub.Forward(sessionID, payload)
				}
			}
			continue
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if wc.tryResolve(env) {
			continue
		}
		envCh <- env
	}

	// Drain the worker before the disconnect sweep so a session.exit that
	// raced the disconnect is applied first (its DB work uses a Background
	// context and finishes on its own).
	close(envCh)
	<-done
	sweepDisconnectedRuntime(context.Background(), db, hub, logger, rt.ID)
}

// sweepDisconnectedRuntime closes out the sessions of a runtime whose
// connection just ended (ADR-050): a session.exit event that would have
// arrived during the disconnect window is silently dropped by the runtime
// (agent/manager.go's send() with no active sender), so those rows would
// otherwise stay requested/starting/running/detached forever and their Hub
// state (scrollback, captures, queues) would never be freed. Command-run
// rows are finalized as failed the same way the exit path would have.
//
// A runtime that reconnects with surviving PTYs is the accepted trade-off:
// its sessions will show failed until they actually exit (recovery for the
// command-run subset is unchanged).
func sweepDisconnectedRuntime(ctx context.Context, db *database.DB, hub *sessions.Hub, logger *slog.Logger, runtimeID string) {
	ids, err := sessions.LiveSessionIDsForRuntime(ctx, db.Pool, runtimeID)
	if err != nil {
		logger.Error("runtime disconnect sweep: list sessions failed", "runtime_id", runtimeID, "error", err)
		return
	}
	for _, id := range ids {
		if err := sessions.MarkFailed(ctx, db.Pool, id, "runtime connection lost"); err != nil {
			logger.Error("runtime disconnect sweep: mark failed failed", "session_id", id, "error", err)
			continue
		}
		if err := commandruns.FinalizeSession(ctx, db.Pool, id, nil, "runtime connection lost", ""); err != nil {
			logger.Warn("command run finalize failed", "session_id", id, "error", err)
		}
		hub.Discard(id)
	}
	if len(ids) > 0 {
		logger.Warn("runtime disconnected with live sessions; swept", "runtime_id", runtimeID, "count", len(ids))
	}
}

// handleRuntimeEnvelope dispatches one control-plane JSON message received
// from a connected runtime.
func handleRuntimeEnvelope(ctx context.Context, env protocol.Envelope, db *database.DB, registry *runtimes.Registry, hub *sessions.Hub, bus *events.Bus, editorHub *editor.Hub, logsHub *dockerLogsHub, logger *slog.Logger, runtimeID string) {
	// A runtime may only act on its own sessions (a compromised one must not
	// be able to end or write into another runtime's sessions).
	owns := func(sessionID string) bool {
		if sessions.BelongsToRuntime(ctx, db.Pool, sessionID, runtimeID) {
			return true
		}
		logger.Warn("runtime message for a session it does not own dropped", "runtime_id", runtimeID, "type", env.Type, "session_id", sessionID)
		return false
	}

	switch env.Type {
	case protocol.MessageHeartbeat:
		if err := runtimes.Heartbeat(ctx, db.Pool, runtimeID); err != nil {
			logger.Error("runtime heartbeat update failed", "runtime_id", runtimeID, "error", err)
		}

	case protocol.MessageSessionReady:
		var p protocol.SessionReadyPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		if !owns(p.SessionID) {
			return
		}
		if err := sessions.MarkDetached(ctx, db.Pool, p.SessionID); err != nil {
			logger.Error("session mark detached failed", "session_id", p.SessionID, "error", err)
		}
		hub.ResolveOpen(p.SessionID, nil)

	case protocol.MessageSessionError:
		var p protocol.SessionErrorPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		if !owns(p.SessionID) {
			return
		}
		if err := sessions.MarkFailed(ctx, db.Pool, p.SessionID, p.Error); err != nil {
			logger.Error("session mark failed failed", "session_id", p.SessionID, "error", err)
		}
		// Finalize a Quick Command run row, if this failed session was one
		// (ADR-045); a no-op for ordinary terminals.
		if err := commandruns.FinalizeSession(ctx, db.Pool, p.SessionID, nil, p.Error, commandruns.Tail(hub.TakeCapture(p.SessionID))); err != nil {
			logger.Warn("command run finalize failed", "session_id", p.SessionID, "error", err)
		}
		hub.ResolveOpen(p.SessionID, errors.New(p.Error))
		hub.Discard(p.SessionID)

	case protocol.MessageSessionExit:
		var p protocol.SessionExitPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		if !owns(p.SessionID) {
			return
		}
		if err := sessions.MarkExited(ctx, db.Pool, p.SessionID, p.ExitCode); err != nil {
			logger.Error("session mark exited failed", "session_id", p.SessionID, "error", err)
		}
		// Finalize the Quick Command run row with the captured output tail
		// (ADR-045); a no-op for ordinary terminals.
		if err := commandruns.FinalizeSession(ctx, db.Pool, p.SessionID, &p.ExitCode, "", commandruns.Tail(hub.TakeCapture(p.SessionID))); err != nil {
			logger.Warn("command run finalize failed", "session_id", p.SessionID, "error", err)
		}
		if sess, err := sessions.Get(ctx, db.Pool, p.SessionID); err == nil {
			if proj, err := projects.Get(ctx, db.Pool, sess.ProjectID); err == nil {
				publishProjectStatus(bus, proj.CreatedBy, proj.ID)
				body := "Exited successfully"
				if p.ExitCode != 0 {
					body = fmt.Sprintf("Exited with code %d", p.ExitCode)
				}
				notifications.Record(ctx, db.Pool, bus, proj.CreatedBy, notifications.TypeTaskCompleted,
					sess.Name+" finished", body,
					map[string]any{"session_id": sess.ID, "project_id": proj.ID, "exit_code": p.ExitCode})
			}
		}
		hub.Discard(p.SessionID)

	case protocol.MessageSessionClosed:
		var p protocol.SessionClosedPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		if !owns(p.SessionID) {
			return
		}
		hub.Discard(p.SessionID)

	case protocol.MessageCodeFileChanged:
		var p protocol.CodeFileChangedPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		// In its own goroutine: it issues a correlated open RPC back to this
		// same runtime, whose reply only this read loop can resolve.
		go handleCodeFileChanged(ctx, db, registry, editorHub, logger, runtimeID, p)

	case protocol.MessageDockerLogs:
		var p protocol.DockerLogsPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		logsHub.forward(p)
	}
}

// handleCodeFileChanged fans one runtime-side file-change push out to the
// project's subscribed editor streams: the change envelope carries only the
// root and path, so the project (and its subscribers) are resolved here,
// and the content is fetched once via a code.command open RPC and then
// published to every subscriber.
func handleCodeFileChanged(ctx context.Context, db *database.DB, registry *runtimes.Registry, editorHub *editor.Hub, logger *slog.Logger, runtimeID string, p protocol.CodeFileChangedPayload) {
	proj, err := projects.GetByRuntimeRoot(ctx, db.Pool, runtimeID, p.Root)
	if err != nil {
		// No editor subscribers exist for a project the pair doesn't resolve
		// to; not even worth a warn for an unbound root.
		logger.Debug("file change push: project resolve failed", "runtime_id", runtimeID, "root", p.Root, "error", err)
		return
	}
	if p.Deleted {
		editorHub.Publish(proj.ID, editor.Message{Type: "deleted", Path: p.Path})
		return
	}
	conn, online := registry.Get(runtimeID)
	if !online {
		return
	}
	out := editorFetch(ctx, conn, p.Root, p.Path)
	if out.Type == "error" {
		logger.Warn("file change push: content fetch failed", "path", p.Path, "error", out.Error)
		editorHub.Publish(proj.ID, editor.Message{Type: "error", Path: p.Path, Error: out.Error})
		return
	}
	editorHub.Publish(proj.ID, editor.Message{Type: out.Type, Path: p.Path, MTime: out.MTime, Size: out.Size, Data: out.Data})
}
