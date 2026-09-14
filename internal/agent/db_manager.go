package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/dbconnectors"
	"devstationone/internal/protocol"
)

// dbCommandTimeout bounds a single database operation (connect + ping, for
// the "test" action).
const dbCommandTimeout = 30 * time.Second

// dbManager implements the db.command half of connection.Manager: it opens
// and tests database connections (internal/dbconnectors) and replies with
// the same correlation ID the request carried, mirroring serviceManager.
type dbManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender
}

func newDBManager(logger *slog.Logger) *dbManager {
	return &dbManager{logger: logger}
}

func (m *dbManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *dbManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one db.command message. It runs the command in its
// own goroutine so a slow database round-trip never blocks the connection's
// read loop.
func (m *dbManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageDBCommand {
		return
	}
	var cmd protocol.DBCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *dbManager) run(id string, cmd protocol.DBCommandPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), dbCommandTimeout)
	defer cancel()

	result, err := executeDBCommand(ctx, cmd)
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageDBError, protocol.DBErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send db error reply failed", "action", cmd.Action, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageDBResult, result); replyErr != nil {
		m.logger.Warn("send db result reply failed", "action", cmd.Action, "error", replyErr)
	}
}

func executeDBCommand(ctx context.Context, cmd protocol.DBCommandPayload) (protocol.DBResultPayload, error) {
	switch cmd.Action {
	case "test":
		connector, err := dbconnectors.For(cmd.Connection.Engine)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		db, err := connector.Open(ctx, toConnectorParams(cmd.Connection))
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		defer db.Close()
		return protocol.DBResultPayload{Ok: true}, nil

	default:
		return protocol.DBResultPayload{}, fmt.Errorf("unknown db action %q", cmd.Action)
	}
}

func toConnectorParams(c protocol.DBConnectionParams) dbconnectors.Params {
	return dbconnectors.Params{
		Engine:       c.Engine,
		Host:         c.Host,
		Port:         c.Port,
		Username:     c.Username,
		Password:     c.Password,
		Database:     c.Database,
		SSLMode:      c.SSLMode,
		InstanceName: c.InstanceName,
		SearchPath:   c.SearchPath,
		ExtraParams:  c.ExtraParams,
	}
}
