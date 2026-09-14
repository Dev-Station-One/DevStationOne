package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/filesystem"
	"devstationone/internal/protocol"
)

// fileCommandTimeout bounds a single filesystem operation, including a
// transfer of protocol.MaxFileBytes over the control-plane connection.
const fileCommandTimeout = 30 * time.Second

// fileManager implements the file.command half of connection.Manager: it
// performs containment-checked filesystem operations (internal/filesystem)
// and replies with the same correlation ID the request carried (see
// runtimes.Conn.SendCommand on the Control Server side).
type fileManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender
}

func newFileManager(logger *slog.Logger) *fileManager {
	return &fileManager{logger: logger}
}

func (m *fileManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *fileManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one file.command message. It runs the operation in
// its own goroutine so a slow call (a large transfer, a deep search) never
// blocks the connection's read loop.
func (m *fileManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageFileCommand {
		return
	}
	var cmd protocol.FileCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *fileManager) run(id string, cmd protocol.FileCommandPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), fileCommandTimeout)
	defer cancel()

	result, err := executeFile(ctx, cmd)
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageFileError, protocol.ServiceErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send file error reply failed", "action", cmd.Action, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageFileResult, result); replyErr != nil {
		m.logger.Warn("send file result reply failed", "action", cmd.Action, "error", replyErr)
	}
}

func executeFile(ctx context.Context, cmd protocol.FileCommandPayload) (protocol.FileResultPayload, error) {
	if cmd.Root == "" {
		return protocol.FileResultPayload{}, fmt.Errorf("missing root directory")
	}
	f, err := filesystem.Open(cmd.Root)
	if err != nil {
		return protocol.FileResultPayload{}, err
	}
	defer f.Close()

	switch cmd.Action {
	case "list":
		entries, err := f.List(cmd.Path)
		if err != nil {
			return protocol.FileResultPayload{}, err
		}
		return protocol.FileResultPayload{Entries: entries}, nil

	case "stat":
		info, err := f.Stat(cmd.Path)
		if err != nil {
			return protocol.FileResultPayload{}, err
		}
		return protocol.FileResultPayload{Info: &info}, nil

	case "read":
		data, err := f.Read(cmd.Path)
		if err != nil {
			return protocol.FileResultPayload{}, err
		}
		return protocol.FileResultPayload{Data: data}, nil

	case "write":
		return protocol.FileResultPayload{}, f.Write(cmd.Path, cmd.Data)

	case "create":
		return protocol.FileResultPayload{}, f.Create(cmd.Path)

	case "mkdir":
		return protocol.FileResultPayload{}, f.Mkdir(cmd.Path)

	case "rename", "move":
		return protocol.FileResultPayload{}, f.Rename(cmd.Path, cmd.To)

	case "copy":
		return protocol.FileResultPayload{}, f.Copy(cmd.Path, cmd.To)

	case "delete":
		return protocol.FileResultPayload{}, f.Delete(cmd.Path)

	case "search":
		matches, err := f.Search(cmd.Path, cmd.Query, cmd.DirsOnly)
		if err != nil {
			return protocol.FileResultPayload{}, err
		}
		return protocol.FileResultPayload{Matches: matches}, nil

	default:
		return protocol.FileResultPayload{}, fmt.Errorf("unknown file action %q", cmd.Action)
	}
}
