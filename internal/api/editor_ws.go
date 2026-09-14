package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/database"
	"devstationone/internal/editor"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// editorReadLimit bounds one browser editor-stream message. A save carries
// a whole file buffer up to protocol.MaxEditorFileBytes as a JSON string
// (text files only, so ~1:1 characters to bytes); the limit is headroom
// above that, far below the runtime control connection's 48 MiB.
const editorReadLimit = 4 << 20

// editorSaveTimeout bounds one correlated save RPC to the runtime, the same
// budget file RPCs get.
const editorSaveTimeout = 30 * time.Second

// editorInMessage is one browser->server message on the editor stream.
type editorInMessage struct {
	Type string `json:"type"` // open | watch | save
	// Paths replaces this connection's open-file set for a "watch" message,
	// relative to the project root.
	Paths []string `json:"paths,omitempty"`
	// Path, Content, BaseMTime and BaseSize carry a "save" (or an "open"'s
	// target): the file (root-relative), the whole buffer, and the client's
	// last-known on-disk fingerprint.
	Path      string    `json:"path,omitempty"`
	Content   string    `json:"content,omitempty"`
	BaseMTime time.Time `json:"base_mtime,omitempty"`
	BaseSize  int64     `json:"base_size,omitempty"`
}

// editorOutMessage is one server->browser message on the editor stream.
type editorOutMessage struct {
	Type string `json:"type"` // content | saved | stale | deleted | error
	Path string `json:"path,omitempty"`
	// MTime/Size report the file's on-disk fingerprint after the change, or
	// (for a "stale") after this client's own write; the client uses them as
	// its next save's base.
	MTime time.Time `json:"mtime,omitempty"`
	Size  int64     `json:"size,omitempty"`
	// Data carries the whole file for "content" and "stale".
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// editorStreamHandler serves the Code tab's live editing stream
// (WSS /api/v1/projects/{projectID}/editor/stream), cookie-authenticated
// like the files endpoints. Browser messages are editorInMessage; the
// server replies per-connection (save results) and fans runtime pushes out
// to every subscribed connection of the project via the editor.Hub.
func editorStreamHandler(db *database.DB, registry *runtimes.Registry, editorHub *editor.Hub, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeConn, project, ok := runtimeConnForProject(w, r, db, registry, r.PathValue("projectID"))
		if !ok {
			return
		}

		conn, err := websocket.Accept(w, r, wsAcceptOptions)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}
		defer conn.CloseNow()
		conn.SetReadLimit(editorReadLimit)

		sub, pushes, cancel := editorHub.Subscribe(project.ID)
		defer cancel()
		// Whatever remains of the project's open-file union after this
		// connection goes is what the runtime should keep watching; an empty
		// union clears the root's whole set.
		defer func() {
			union := editorHub.Drop(project.ID, sub)
			_ = runtimeConn.SendEnvelope(protocol.MessageCodeCommand, protocol.CodeCommandPayload{
				Action: "unwatch", Root: project.WorkingDirectory, Paths: union,
			})
		}()

		ctx := r.Context()

		// Save replies and watch updates from the reader land here so one
		// writer loop serializes everything onto the connection.
		out := make(chan editorOutMessage, 16)

		closed := make(chan struct{})
		go func() {
			defer close(closed)
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var in editorInMessage
				if json.Unmarshal(data, &in) != nil {
					continue
				}
				switch in.Type {
				case "watch":
					union := editorHub.SetPaths(project.ID, sub, in.Paths)
					if err := runtimeConn.SendEnvelope(protocol.MessageCodeCommand, protocol.CodeCommandPayload{
						Action: "watch", Root: project.WorkingDirectory, Paths: union,
					}); err != nil {
						logger.Warn("editor watch send failed", "project_id", project.ID, "error", err)
					}
				case "open":
					go func(in editorInMessage) {
						out <- editorFetch(ctx, runtimeConn, project.WorkingDirectory, in.Path)
					}(in)
				case "save":
					go func(in editorInMessage) {
						out <- editorSave(ctx, runtimeConn, project.WorkingDirectory, in)
					}(in)
				}
			}
		}()

		ticker := time.NewTicker(eventsPingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-closed:
				return
			case m := <-pushes:
				if !writeEditorMessage(ctx, conn, editorOutMessage{
					Type: m.Type, Path: m.Path, MTime: m.MTime, Size: m.Size, Data: m.Data,
				}) {
					return
				}
			case m := <-out:
				if !writeEditorMessage(ctx, conn, m) {
					return
				}
			case <-ticker.C:
				if !writeEditorMessage(ctx, conn, editorOutMessage{Type: "ping"}) {
					return
				}
			}
		}
	}
}

// editorFetch issues one correlated open RPC and translates the runtime's
// reply into the per-connection browser message (content, or an error for
// missing/oversized/binary files).
func editorFetch(ctx context.Context, runtimeConn runtimes.Conn, root, path string) editorOutMessage {
	openCtx, cancel := context.WithTimeout(ctx, editorSaveTimeout)
	defer cancel()
	raw, err := runtimeConn.SendCommand(openCtx, protocol.MessageCodeCommand, protocol.CodeCommandPayload{
		Action: "open", Root: root, Path: path,
	})
	if err != nil {
		return editorOutMessage{Type: "error", Path: path, Error: err.Error()}
	}
	var result protocol.CodeResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		return editorOutMessage{Type: "error", Path: path, Error: "invalid response from runtime"}
	}
	if result.Binary {
		return editorOutMessage{Type: "error", Path: path, Error: "file is binary"}
	}
	return editorOutMessage{Type: "content", Path: path, MTime: result.MTime, Size: result.Size, Data: string(result.Data)}
}

// editorSave issues one correlated save RPC and translates the runtime's
// reply into the per-connection browser message.
func editorSave(ctx context.Context, runtimeConn runtimes.Conn, root string, in editorInMessage) editorOutMessage {
	cmd := protocol.CodeCommandPayload{
		Action:    "save",
		Root:      root,
		Path:      in.Path,
		Data:      []byte(in.Content),
		BaseMTime: in.BaseMTime,
		BaseSize:  in.BaseSize,
	}
	cctx, cancel := context.WithTimeout(ctx, editorSaveTimeout)
	defer cancel()
	raw, err := runtimeConn.SendCommand(cctx, protocol.MessageCodeCommand, cmd)
	if err != nil {
		msg := editorOutMessage{Type: "error", Path: in.Path, Error: err.Error()}
		if ctx.Err() != nil {
			msg.Error = "timed out waiting for runtime"
		}
		return msg
	}
	var result protocol.CodeResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		return editorOutMessage{Type: "error", Path: in.Path, Error: "invalid response from runtime"}
	}
	if result.Stale {
		return editorOutMessage{Type: "stale", Path: in.Path, MTime: result.MTime, Size: result.Size, Data: string(result.Data)}
	}
	return editorOutMessage{Type: "saved", Path: in.Path, MTime: result.MTime, Size: result.Size}
}

func writeEditorMessage(ctx context.Context, conn *websocket.Conn, m editorOutMessage) bool {
	data, err := json.Marshal(m)
	if err != nil {
		return true // skip a malformed message, don't kill the connection over it
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data) == nil
}
