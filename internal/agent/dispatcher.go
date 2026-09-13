package agent

import (
	"log/slog"

	"devstation/internal/connection"
	"devstation/internal/protocol"
)

// dispatcher implements connection.Manager by routing each control-plane
// message to whichever capability handles it: service.command to
// serviceManager, everything else (session.*, revoked) to sessionManager.
// Terminal frames always belong to a session.
type dispatcher struct {
	session *sessionManager
	service *serviceManager
}

// NewManager builds the runtime's top-level connection.Manager, driving
// both real PTY sessions (Slice 4) and systemd --user services (Slice 5)
// from control-plane messages. Exported so integration tests can run the
// real runtime-side logic against a real PTY/systemd, without exec'ing the
// devstation-runtime binary as a subprocess.
func NewManager(logger *slog.Logger) connection.Manager {
	return &dispatcher{
		session: newSessionManager(logger),
		service: newServiceManager(logger),
	}
}

func (d *dispatcher) SetSender(s connection.Sender) {
	d.session.SetSender(s)
	d.service.SetSender(s)
}

func (d *dispatcher) HandleEnvelope(env protocol.Envelope) {
	if env.Type == protocol.MessageServiceCommand {
		d.service.HandleEnvelope(env)
		return
	}
	d.session.HandleEnvelope(env)
}

func (d *dispatcher) HandleFrame(sessionID string, data []byte) {
	d.session.HandleFrame(sessionID, data)
}
