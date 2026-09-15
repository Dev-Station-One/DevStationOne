package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// dockerRPCTimeout bounds a container/resources RPC (compose up/down gets
// its own, longer bound — see dockerComposeRPCTimeout).
const dockerRPCTimeout = 15 * time.Second

// dockerComposeRPCTimeout covers the runtime-side 10-minute compose bound
// plus reply margin.
const dockerComposeRPCTimeout = 11 * time.Minute

// dockerLogsTail is the past-line replay a followed log stream starts with.
const dockerLogsTail = 200

// validContainerID matches Docker's own container name/ID charset; it stops
// path tricks and empty targets before anything reaches the runtime (which
// re-validates scope independently, ADR-040).
var validContainerID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// dockerLogsHub routes runtime-pushed docker.logs batches to the browser
// stream waiting for them, keyed by stream ID. Scoped to one Control Server
// process, like sessions.Hub.
type dockerLogsHub struct {
	mu      sync.Mutex
	streams map[string]chan protocol.DockerLogsPayload
}

func newDockerLogsHub() *dockerLogsHub {
	return &dockerLogsHub{streams: make(map[string]chan protocol.DockerLogsPayload)}
}

// register returns the channel streamID's pushes arrive on.
func (h *dockerLogsHub) register(streamID string) chan protocol.DockerLogsPayload {
	ch := make(chan protocol.DockerLogsPayload, 16)
	h.mu.Lock()
	h.streams[streamID] = ch
	h.mu.Unlock()
	return ch
}

// unregister drops streamID's channel.
func (h *dockerLogsHub) unregister(streamID string) {
	h.mu.Lock()
	delete(h.streams, streamID)
	h.mu.Unlock()
}

// forward delivers one batch to its stream, dropping it when the browser is
// slow (log tailing tolerates loss far better than a blocked read loop).
func (h *dockerLogsHub) forward(p protocol.DockerLogsPayload) {
	h.mu.Lock()
	ch, ok := h.streams[p.StreamID]
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- p:
	default:
	}
}

// dockerCommand issues a docker.command RPC and decodes its result,
// writing an appropriate error response on failure.
func dockerCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.DockerCommandPayload, timeout time.Duration) (json.RawMessage, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	raw, err := conn.SendCommand(ctx, protocol.MessageDockerCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return nil, false
	}
	return raw, true
}

// dockerProjectConn resolves the project's runtime connection for a docker
// request, writing an error response and returning ok=false on failure.
func dockerProjectConn(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry) (runtimes.Conn, string, bool) {
	conn, project, ok := runtimeConnForProject(w, r, db, registry, r.PathValue("projectID"))
	if !ok {
		return nil, "", false
	}
	return conn, project.WorkingDirectory, true
}

// getDockerStatusHandler serves the Docker tab's first-render payload:
// availability, compose detection, and the scoped container list.
func getDockerStatusHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := dockerProjectConn(w, r, db, registry)
		if !ok {
			return
		}
		raw, ok := dockerCommand(w, r, conn, protocol.DockerCommandPayload{Action: "list", Root: root}, dockerRPCTimeout)
		if !ok {
			return
		}
		var st protocol.DockerStatusPayload
		if err := json.Unmarshal(raw, &st); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		if st.Containers == nil {
			st.Containers = []protocol.ContainerInfo{}
		}
		writeJSON(w, http.StatusOK, st)
	}
}

// dockerContainerActionHandler runs one container lifecycle action
// (start/stop/restart/remove) selected by the {dockerAction} path value,
// requiring no request body, returning the refreshed scoped list.
func dockerContainerActionHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := r.PathValue("dockerAction")
		switch action {
		case "start", "stop", "restart", "remove":
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown container action"})
			return
		}
		conn, root, ok := dockerProjectConn(w, r, db, registry)
		if !ok {
			return
		}
		containerID := r.PathValue("containerID")
		if !validContainerID.MatchString(containerID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid container id"})
			return
		}
		cmd := protocol.DockerCommandPayload{Action: action, Root: root, Container: containerID}
		if action == "remove" {
			var body struct {
				RemoveVolumes bool `json:"remove_volumes"`
			}
			if r.ContentLength > 0 && json.NewDecoder(r.Body).Decode(&body) == nil {
				cmd.RemoveVolumes = body.RemoveVolumes
			}
		}
		raw, ok := dockerCommand(w, r, conn, cmd, dockerRPCTimeout)
		if !ok {
			return
		}
		var result protocol.DockerStatusPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		if result.Containers == nil {
			result.Containers = []protocol.ContainerInfo{}
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventDockerAction, audit.Actor{UserID: user.ID, Email: user.Email}, "container", containerID, map[string]any{"action": action, "project_id": r.PathValue("projectID")}, clientIP(r))
		writeJSON(w, http.StatusOK, result)
	}
}

// dockerComposeHandler runs one compose action (up/down/stop/restart)
// selected by the {dockerAction} path value, returning the bounded CLI
// output tail.
func dockerComposeHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := r.PathValue("dockerAction")
		switch action {
		case "up", "down", "stop", "restart":
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown compose action"})
			return
		}
		conn, root, ok := dockerProjectConn(w, r, db, registry)
		if !ok {
			return
		}
		raw, ok := dockerCommand(w, r, conn, protocol.DockerCommandPayload{
			Action: "compose_" + action, Root: root,
		}, dockerComposeRPCTimeout)
		if !ok {
			return
		}
		var result protocol.DockerComposeOutputPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventDockerAction, audit.Actor{UserID: user.ID, Email: user.Email}, "compose", action, map[string]any{"action": action, "project_id": r.PathValue("projectID")}, clientIP(r))
		writeJSON(w, http.StatusOK, result)
	}
}

// getDockerResourcesHandler serves the read-only image/volume/network
// lists referenced by the project's scoped containers.
func getDockerResourcesHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := dockerProjectConn(w, r, db, registry)
		if !ok {
			return
		}
		raw, ok := dockerCommand(w, r, conn, protocol.DockerCommandPayload{Action: "resources", Root: root}, dockerRPCTimeout)
		if !ok {
			return
		}
		var result protocol.DockerResourcesPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// dockerLogsStreamHandler upgrades to a WebSocket, starts a followed log
// stream on the runtime, and relays pushed batches until the browser
// disconnects or the stream ends. On disconnect it stops the runtime-side
// follow.
func dockerLogsStreamHandler(db *database.DB, registry *runtimes.Registry, logsHub *dockerLogsHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := dockerProjectConn(w, r, db, registry)
		if !ok {
			return
		}
		containerID := r.PathValue("containerID")
		if !validContainerID.MatchString(containerID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid container id"})
			return
		}

		ws, err := websocket.Accept(w, r, wsAcceptOptions)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}
		defer ws.CloseNow()

		streamID, err := auth.NewSessionToken()
		if err != nil {
			return
		}
		ch := logsHub.register(streamID)
		defer logsHub.unregister(streamID)

		startCtx, startCancel := context.WithTimeout(context.Background(), dockerRPCTimeout)
		_, startErr := conn.SendCommand(startCtx, protocol.MessageDockerCommand, protocol.DockerCommandPayload{
			Action: "logs_start", Root: root, Container: containerID, StreamID: streamID, Tail: dockerLogsTail,
		})
		startCancel()
		if startErr != nil {
			return
		}
		defer func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = conn.SendCommand(stopCtx, protocol.MessageDockerCommand, protocol.DockerCommandPayload{
				Action: "logs_stop", StreamID: streamID,
			})
			stopCancel()
		}()

		writeCtx := r.Context()
		for {
			select {
			case <-writeCtx.Done():
				return
			case p := <-ch:
				data, err := json.Marshal(p)
				if err != nil {
					return
				}
				wctx, wcancel := context.WithTimeout(writeCtx, 10*time.Second)
				err = ws.Write(wctx, websocket.MessageText, data)
				wcancel()
				if err != nil {
					return
				}
				if p.End {
					// Follow ended (container removed, retained log over);
					// the runtime will not push more for this stream —
					// close the browser side cleanly.
					return
				}
			}
		}
	}
}
