package agent

import (
	"encoding/json"
	"log/slog"
	"sync"
	"syscall"

	"devstationone/internal/connection"
	"devstationone/internal/protocol"
	"devstationone/internal/pty"
)

// posixSignal maps the POSIX signal names the Control Server may send in a
// session.signal command to their syscall.Signal value. Only the signals a
// terminal session realistically needs are supported.
func posixSignal(name string) (syscall.Signal, bool) {
	switch name {
	case "SIGINT":
		return syscall.SIGINT, true
	case "SIGTERM":
		return syscall.SIGTERM, true
	case "SIGKILL":
		return syscall.SIGKILL, true
	case "SIGHUP":
		return syscall.SIGHUP, true
	case "SIGQUIT":
		return syscall.SIGQUIT, true
	case "SIGTSTP":
		return syscall.SIGTSTP, true
	case "SIGCONT":
		return syscall.SIGCONT, true
	default:
		return 0, false
	}
}

// sessionManager implements connection.Manager, starting and driving real
// PTY sessions (internal/pty) in response to control-plane messages, and
// streaming their output back over whichever connection is currently
// active. It outlives any single connection so sessions survive a
// reconnect (PROTOCOL.md: "A detached browser does not imply an exited
// process" — the same holds for a runtime<->server network blip).
type sessionManager struct {
	logger *slog.Logger

	mu       sync.Mutex
	sender   connection.Sender
	sessions map[string]*pty.Session
}

// newSessionManager builds the terminal-session half of the runtime's
// connection.Manager: it starts and drives real PTY sessions in response to
// control-plane messages from the Control Server.
func newSessionManager(logger *slog.Logger) *sessionManager {
	return &sessionManager{logger: logger, sessions: make(map[string]*pty.Session)}
}

func (m *sessionManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *sessionManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

func (m *sessionManager) send(msgType protocol.MessageType, payload any) {
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err := sender.SendEnvelope(msgType, payload); err != nil {
		m.logger.Warn("send control message failed", "type", msgType, "error", err)
	}
}

func (m *sessionManager) sendFrame(sessionID string, data []byte) {
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err := sender.SendFrame(sessionID, data); err != nil {
		m.logger.Warn("send output frame failed", "session_id", sessionID, "error", err)
	}
}

func (m *sessionManager) HandleEnvelope(env protocol.Envelope) {
	switch env.Type {
	case protocol.MessageRevoked:
		m.logger.Warn("runtime credential revoked by control server")

	case protocol.MessageSessionOpen:
		var p protocol.SessionOpenPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		m.open(p)

	case protocol.MessageSessionResize:
		var p protocol.SessionResizePayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		m.withSession(p.SessionID, func(s *pty.Session) {
			if err := s.Resize(p.Cols, p.Rows); err != nil {
				m.logger.Warn("resize failed", "session_id", p.SessionID, "error", err)
			}
		})

	case protocol.MessageSessionSignal:
		var p protocol.SessionSignalPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		sig, ok := posixSignal(p.Signal)
		if !ok {
			m.logger.Warn("unknown signal requested", "session_id", p.SessionID, "signal", p.Signal)
			return
		}
		m.withSession(p.SessionID, func(s *pty.Session) {
			if err := s.Signal(sig); err != nil {
				m.logger.Warn("signal failed", "session_id", p.SessionID, "error", err)
			}
		})

	case protocol.MessageSessionClose:
		var p protocol.SessionClosePayload
		if json.Unmarshal(env.Payload, &p) != nil {
			return
		}
		m.closeSession(p.SessionID)
		m.send(protocol.MessageSessionClosed, protocol.SessionClosedPayload{SessionID: p.SessionID})
	}
}

func (m *sessionManager) HandleFrame(sessionID string, data []byte) {
	m.withSession(sessionID, func(s *pty.Session) {
		if _, err := s.Write(data); err != nil {
			m.logger.Warn("pty write failed", "session_id", sessionID, "error", err)
		}
	})
}

func (m *sessionManager) withSession(sessionID string, fn func(*pty.Session)) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	m.mu.Unlock()
	if s != nil {
		fn(s)
	}
}

func (m *sessionManager) open(p protocol.SessionOpenPayload) {
	s, err := pty.Start(pty.Options{
		Shell:  p.Shell,
		Dir:    p.Dir,
		OSUser: p.OSUser,
		Env:    p.Env,
		Cols:   p.Cols,
		Rows:   p.Rows,
	})
	if err != nil {
		m.send(protocol.MessageSessionError, protocol.SessionErrorPayload{SessionID: p.SessionID, Error: err.Error()})
		return
	}

	m.mu.Lock()
	m.sessions[p.SessionID] = s
	m.mu.Unlock()

	m.send(protocol.MessageSessionReady, protocol.SessionReadyPayload{SessionID: p.SessionID})

	go m.pump(p.SessionID, s)
}

// pump copies PTY output to the Control Server until the process exits,
// then reports the exit code and forgets the session.
func (m *sessionManager) pump(sessionID string, s *pty.Session) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			m.sendFrame(sessionID, append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			break
		}
	}

	exitCode := s.Wait()

	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	m.send(protocol.MessageSessionExit, protocol.SessionExitPayload{SessionID: sessionID, ExitCode: exitCode})
}

func (m *sessionManager) closeSession(sessionID string) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}
