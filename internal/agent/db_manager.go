package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/dbconnectors"
	"devstationone/internal/protocol"
)

// dbCommandTimeout bounds a single database RPC (connect, ping, or one
// schema-introspection query).
const dbCommandTimeout = 30 * time.Second

// dbPoolIdleTimeout is how long a cached connection pool sits unused before
// dbManager closes it. Schema browsing fires many small RPCs against the
// same connection in quick succession (expanding a tree node), so reusing
// an already-open *sql.DB avoids a fresh handshake per click; see
// ADR-021/022.
const dbPoolIdleTimeout = 5 * time.Minute

// dbCachedPool is one entry in dbManager.pools.
type dbCachedPool struct {
	db       *sql.DB
	lastUsed time.Time
}

// dbManager implements the db.command half of connection.Manager: it opens,
// tests, and introspects database connections (internal/dbconnectors),
// caching one connection pool per ConnectionID, and replies with the same
// correlation ID the request carried, mirroring serviceManager.
type dbManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender

	poolMu sync.Mutex
	pools  map[string]*dbCachedPool
}

func newDBManager(logger *slog.Logger) *dbManager {
	return &dbManager{logger: logger, pools: make(map[string]*dbCachedPool)}
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

// getPool returns the cached *sql.DB for id, opening one if there is none
// yet. Idle entries for other connections are swept (closed and dropped) on
// every call — cheap for the handful of connections a runtime realistically
// has open at once, and avoids a separate background goroutine.
func (m *dbManager) getPool(ctx context.Context, id string, p dbconnectors.Params) (*sql.DB, error) {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()

	now := time.Now()
	for cid, cp := range m.pools {
		if cid != id && now.Sub(cp.lastUsed) > dbPoolIdleTimeout {
			cp.db.Close()
			delete(m.pools, cid)
		}
	}

	if cp, ok := m.pools[id]; ok {
		cp.lastUsed = now
		return cp.db, nil
	}

	connector, err := dbconnectors.For(p.Engine)
	if err != nil {
		return nil, err
	}
	db, err := connector.Open(ctx, p)
	if err != nil {
		return nil, err
	}
	m.pools[id] = &dbCachedPool{db: db, lastUsed: now}
	return db, nil
}

// invalidate closes and drops id's cached pool, if any — sent by the
// Control Server after a connection's stored credentials change or it's
// deleted, so a cached pool never outlives its (possibly now-wrong)
// credentials by more than one edit.
func (m *dbManager) invalidate(id string) {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	if cp, ok := m.pools[id]; ok {
		cp.db.Close()
		delete(m.pools, id)
	}
}

// HandleEnvelope handles one db.command message. "invalidate" is handled
// inline (no reply expected); every other action runs in its own goroutine
// so a slow database round-trip never blocks the connection's read loop.
func (m *dbManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageDBCommand {
		return
	}
	var cmd protocol.DBCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	if cmd.Action == "invalidate" {
		m.invalidate(cmd.ConnectionID)
		return
	}
	go m.run(env.ID, cmd)
}

func (m *dbManager) run(id string, cmd protocol.DBCommandPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), dbCommandTimeout)
	defer cancel()

	result, err := m.execute(ctx, cmd)
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

func (m *dbManager) execute(ctx context.Context, cmd protocol.DBCommandPayload) (protocol.DBResultPayload, error) {
	params := toConnectorParams(cmd.Connection)

	if cmd.Action == "test" {
		db, err := m.getPool(ctx, cmd.ConnectionID, params)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		if err := db.PingContext(ctx); err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Ok: true}, nil
	}

	db, err := m.getPool(ctx, cmd.ConnectionID, params)
	if err != nil {
		return protocol.DBResultPayload{}, err
	}
	connector, err := dbconnectors.For(cmd.Connection.Engine)
	if err != nil {
		return protocol.DBResultPayload{}, err
	}

	switch cmd.Action {
	case "schemas":
		schemas, err := connector.Schemas(ctx, db, params)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Schemas: schemas}, nil

	case "tables":
		tables, err := connector.Tables(ctx, db, cmd.Schema)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Tables: toProtocolTables(tables)}, nil

	case "columns":
		cols, err := connector.Columns(ctx, db, cmd.Schema, cmd.Table)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Columns: toProtocolColumns(cols)}, nil

	case "indexes":
		idx, err := connector.Indexes(ctx, db, cmd.Schema, cmd.Table)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Indexes: toProtocolIndexes(idx)}, nil

	case "constraints":
		cons, err := connector.Constraints(ctx, db, cmd.Schema, cmd.Table)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Constraints: toProtocolConstraints(cons)}, nil

	case "triggers":
		trig, err := connector.Triggers(ctx, db, cmd.Schema, cmd.Table)
		if err != nil {
			return protocol.DBResultPayload{}, err
		}
		return protocol.DBResultPayload{Triggers: toProtocolTriggers(trig)}, nil

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

func toProtocolTables(in []dbconnectors.Table) []protocol.DBTableInfo {
	out := make([]protocol.DBTableInfo, 0, len(in))
	for _, t := range in {
		out = append(out, protocol.DBTableInfo{Name: t.Name, Kind: t.Kind})
	}
	return out
}

func toProtocolColumns(in []dbconnectors.Column) []protocol.DBColumnInfo {
	out := make([]protocol.DBColumnInfo, 0, len(in))
	for _, c := range in {
		out = append(out, protocol.DBColumnInfo{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Default: c.Default, Position: c.Position})
	}
	return out
}

func toProtocolIndexes(in []dbconnectors.Index) []protocol.DBIndexInfo {
	out := make([]protocol.DBIndexInfo, 0, len(in))
	for _, idx := range in {
		out = append(out, protocol.DBIndexInfo{Name: idx.Name, Columns: idx.Columns, Unique: idx.Unique, Primary: idx.Primary})
	}
	return out
}

func toProtocolConstraints(in []dbconnectors.Constraint) []protocol.DBConstraintInfo {
	out := make([]protocol.DBConstraintInfo, 0, len(in))
	for _, c := range in {
		out = append(out, protocol.DBConstraintInfo{Name: c.Name, Type: c.Type, Columns: c.Columns, References: c.References})
	}
	return out
}

func toProtocolTriggers(in []dbconnectors.Trigger) []protocol.DBTriggerInfo {
	out := make([]protocol.DBTriggerInfo, 0, len(in))
	for _, t := range in {
		out = append(out, protocol.DBTriggerInfo{Name: t.Name, Event: t.Event, Timing: t.Timing})
	}
	return out
}
