package agent

import (
	"log/slog"

	"devstationone/internal/connection"
	"devstationone/internal/protocol"
)

// dispatcher implements connection.Manager by routing each control-plane
// message to whichever capability handles it: service.command to
// serviceManager, file.command to fileManager, db.command to dbManager,
// git.command to gitManager, code.command to editManager, everything else
// (session.*, revoked) to sessionManager. Terminal frames always belong to
// a session.
type dispatcher struct {
	session *sessionManager
	service *serviceManager
	file    *fileManager
	db      *dbManager
	git     *gitManager
	edit    *editManager
}

// NewManager builds the runtime's top-level connection.Manager, driving
// real PTY sessions (Slice 4), systemd --user services (Slice 5), contained
// filesystem operations (Slice 6), database connections (Slice 7), git
// operations (Slice 16), and editor open/save/watch operations (Code tab)
// from control-plane messages. Exported so
// integration tests can run the real runtime-side logic against a real
// PTY/systemd, without exec'ing the devstationone-runtime binary as a
// subprocess.
func NewManager(logger *slog.Logger) connection.Manager {
	return &dispatcher{
		session: newSessionManager(logger),
		service: newServiceManager(logger),
		file:    newFileManager(logger),
		db:      newDBManager(logger),
		git:     newGitManager(logger),
		edit:    newEditManager(logger),
	}
}

func (d *dispatcher) SetSender(s connection.Sender) {
	d.session.SetSender(s)
	d.service.SetSender(s)
	d.file.SetSender(s)
	d.db.SetSender(s)
	d.git.SetSender(s)
	d.edit.SetSender(s)
}

func (d *dispatcher) HandleEnvelope(env protocol.Envelope) {
	if env.Type == protocol.MessageServiceCommand {
		d.service.HandleEnvelope(env)
		return
	}
	if env.Type == protocol.MessageFileCommand {
		d.file.HandleEnvelope(env)
		return
	}
	if env.Type == protocol.MessageDBCommand {
		d.db.HandleEnvelope(env)
		return
	}
	if env.Type == protocol.MessageGitCommand {
		d.git.HandleEnvelope(env)
		return
	}
	if env.Type == protocol.MessageCodeCommand {
		d.edit.HandleEnvelope(env)
		return
	}
	d.session.HandleEnvelope(env)
}

func (d *dispatcher) HandleFrame(sessionID string, data []byte) {
	d.session.HandleFrame(sessionID, data)
}
