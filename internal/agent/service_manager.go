package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"devstation/internal/connection"
	"devstation/internal/protocol"
	"devstation/internal/services"
)

// serviceCommandTimeout bounds a single systemctl/journalctl invocation.
const serviceCommandTimeout = 15 * time.Second

// serviceManager implements the service.command half of connection.Manager:
// it runs systemd --user operations (internal/services) and replies with
// the same correlation ID the request carried (see
// runtimes.Conn.SendCommand on the Control Server side).
type serviceManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender
}

func newServiceManager(logger *slog.Logger) *serviceManager {
	return &serviceManager{logger: logger}
}

func (m *serviceManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *serviceManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one service.command message. It runs the command
// in its own goroutine so a slow systemctl/journalctl call never blocks the
// connection's read loop (and other requests in flight on it).
func (m *serviceManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageServiceCommand {
		return
	}
	var cmd protocol.ServiceCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *serviceManager) run(id string, cmd protocol.ServiceCommandPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceCommandTimeout)
	defer cancel()

	result, err := execute(ctx, cmd)
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageServiceError, protocol.ServiceErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send service error reply failed", "action", cmd.Action, "unit", cmd.Unit, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageServiceResult, result); replyErr != nil {
		m.logger.Warn("send service result reply failed", "action", cmd.Action, "unit", cmd.Unit, "error", replyErr)
	}
}

func execute(ctx context.Context, cmd protocol.ServiceCommandPayload) (protocol.ServiceResultPayload, error) {
	switch cmd.Action {
	case "list":
		units, err := services.List(ctx)
		if err != nil {
			return protocol.ServiceResultPayload{}, err
		}
		return protocol.ServiceResultPayload{Services: toProtocolUnits(units)}, nil

	case "status":
		u, err := services.Status(ctx, cmd.Unit)
		if err != nil {
			return protocol.ServiceResultPayload{}, err
		}
		pu := toProtocolUnit(u)
		return protocol.ServiceResultPayload{Service: &pu}, nil

	case "start", "stop", "restart", "enable", "disable":
		u, err := runServiceAction(ctx, cmd.Action, cmd.Unit)
		if err != nil {
			return protocol.ServiceResultPayload{}, err
		}
		pu := toProtocolUnit(u)
		return protocol.ServiceResultPayload{Service: &pu}, nil

	case "logs":
		lines, err := services.Logs(ctx, cmd.Unit, cmd.Lines)
		if err != nil {
			return protocol.ServiceResultPayload{}, err
		}
		return protocol.ServiceResultPayload{Logs: lines}, nil

	default:
		return protocol.ServiceResultPayload{}, fmt.Errorf("unknown service action %q", cmd.Action)
	}
}

func runServiceAction(ctx context.Context, action, unit string) (services.Unit, error) {
	switch action {
	case "start":
		return services.Start(ctx, unit)
	case "stop":
		return services.Stop(ctx, unit)
	case "restart":
		return services.Restart(ctx, unit)
	case "enable":
		return services.Enable(ctx, unit)
	case "disable":
		return services.Disable(ctx, unit)
	}
	return services.Unit{}, fmt.Errorf("unknown service action %q", action)
}

func toProtocolUnit(u services.Unit) protocol.ServiceInfo {
	return protocol.ServiceInfo{
		Unit:        u.Name,
		Description: u.Description,
		LoadState:   u.LoadState,
		ActiveState: u.ActiveState,
		SubState:    u.SubState,
		Enabled:     u.Enabled,
	}
}

func toProtocolUnits(units []services.Unit) []protocol.ServiceInfo {
	out := make([]protocol.ServiceInfo, 0, len(units))
	for _, u := range units {
		out = append(out, toProtocolUnit(u))
	}
	return out
}
