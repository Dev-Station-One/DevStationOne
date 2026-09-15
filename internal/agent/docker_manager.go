package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/docker"
	"devstationone/internal/protocol"
)

// dockerCommandTimeout bounds one container-lifecycle or resources RPC.
// compose up/down carry their own 10-minute bound (internal/docker).
const dockerCommandTimeout = 15 * time.Second

// dockerLogFlush coalesces followed log lines into batches.
const dockerLogFlush = 200 * time.Millisecond

// dockerManager implements the docker.command half of connection.Manager
// (ADR-040), same reply discipline as serviceManager.
type dockerManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender

	// logsMu guards streams: one follow goroutine per logs_start, cancelled
	// by logs_stop.
	logsMu  sync.Mutex
	streams map[string]context.CancelFunc
}

func newDockerManager(logger *slog.Logger) *dockerManager {
	return &dockerManager{logger: logger, streams: make(map[string]context.CancelFunc)}
}

func (m *dockerManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *dockerManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one docker.command message, running the command in
// its own goroutine so a slow compose build never blocks the read loop.
func (m *dockerManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageDockerCommand {
		return
	}
	var cmd protocol.DockerCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *dockerManager) run(id string, cmd protocol.DockerCommandPayload) {
	if cmd.Action == "logs_start" {
		m.startLogs(cmd)
		if sender := m.currentSender(); sender != nil {
			_ = sender.SendReply(id, protocol.MessageDockerResult, protocol.DockerLogsPayload{StreamID: cmd.StreamID})
		}
		return
	}
	if cmd.Action == "logs_stop" {
		m.stopLogs(cmd.StreamID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()

	var result any
	var err error
	switch cmd.Action {
	case "list":
		var st protocol.DockerStatusPayload
		st, err = m.status(ctx, cmd.Root)
		result = st
	case "resources":
		var res protocol.DockerResourcesPayload
		res, err = docker.Resources(ctx, cmd.Root)
		result = res
	case "start", "stop", "restart", "remove":
		var list []protocol.ContainerInfo
		list, err = docker.Action(ctx, cmd.Root, cmd.Container, cmd.Action, cmd.RemoveVolumes)
		result = protocol.DockerStatusPayload{Containers: list}
	case "compose_up", "compose_down", "compose_stop", "compose_restart":
		var out string
		out, err = docker.Compose(ctx, cmd.Root, strings.TrimPrefix(cmd.Action, "compose_"))
		result = protocol.DockerComposeOutputPayload{Output: out}
	default:
		err = fmt.Errorf("unknown docker action %q", cmd.Action)
	}

	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageDockerError, protocol.ServiceErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send docker error reply failed", "action", cmd.Action, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageDockerResult, result); replyErr != nil {
		m.logger.Warn("send docker result reply failed", "action", cmd.Action, "error", replyErr)
	}
}

// status is the Docker tab's first-render payload: capability, compose file
// detection, and the scoped container list in one shot.
func (m *dockerManager) status(ctx context.Context, root string) (protocol.DockerStatusPayload, error) {
	st := protocol.DockerStatusPayload{}
	cap := docker.Discover(ctx)
	st.Available = cap.Available
	st.Version = cap.Version
	st.Compose = cap.Compose
	if !st.Available {
		return st, nil
	}
	st.ComposeFile = docker.ComposeFile(root)
	containers, err := docker.List(ctx, root)
	if err != nil {
		return st, nil
	}
	st.Containers = containers
	return st, nil
}

// startLogs runs one FollowLogs goroutine for StreamID, batching lines into
// MessageDockerLogs pushes. A prior stream with the same StreamID is
// cancelled first.
func (m *dockerManager) startLogs(cmd protocol.DockerCommandPayload) {
	m.stopLogs(cmd.StreamID)

	ctx, cancel := context.WithCancel(context.Background())
	m.logsMu.Lock()
	m.streams[cmd.StreamID] = cancel
	m.logsMu.Unlock()

	go func() {
		defer func() {
			m.logsMu.Lock()
			delete(m.streams, cmd.StreamID)
			m.logsMu.Unlock()
		}()

		lines := make(chan string, 100)
		go func() {
			defer close(lines)
			_ = docker.FollowLogs(ctx, cmd.Root, cmd.Container, cmd.Tail, func(line string) {
				select {
				case lines <- line:
				case <-ctx.Done():
				}
			})
		}()

		ticker := time.NewTicker(dockerLogFlush)
		defer ticker.Stop()

		var batch []string
		flush := func(ls []string, end bool) {
			if len(ls) == 0 && !end {
				return
			}
			sender := m.currentSender()
			if sender == nil {
				return
			}
			if err := sender.SendEnvelope(protocol.MessageDockerLogs, protocol.DockerLogsPayload{
				StreamID: cmd.StreamID, Lines: ls, End: end,
			}); err != nil {
				m.logger.Warn("send docker logs push failed", "stream", cmd.StreamID, "error", err)
			}
		}

		for {
			select {
			case line, ok := <-lines:
				if !ok {
					flush(batch, true)
					return
				}
				batch = append(batch, line)
				if len(batch) >= 100 {
					flush(batch, false)
					batch = nil
				}
			case <-ticker.C:
				flush(batch, false)
				batch = nil
			case <-ctx.Done():
				flush(batch, true)
				return
			}
		}
	}()
}

// stopLogs cancels the follow goroutine for StreamID, if any.
func (m *dockerManager) stopLogs(streamID string) {
	m.logsMu.Lock()
	cancel, ok := m.streams[streamID]
	delete(m.streams, streamID)
	m.logsMu.Unlock()
	if ok {
		cancel()
	}
}
