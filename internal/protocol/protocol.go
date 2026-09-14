// Package protocol defines the versioned control-plane message envelope
// exchanged over the Control Server <-> Development Runtime WebSocket
// connection. Terminal/file/service/database payloads are added in the
// slices that implement them.
package protocol

import (
	"encoding/json"
	"time"
)

// Version is the current control-plane protocol version.
const Version = "1"

// MessageType identifies the kind of payload carried by an Envelope.
type MessageType string

const (
	// MessageHello is sent once by the runtime immediately after connecting,
	// advertising its identity and capabilities.
	MessageHello MessageType = "hello"
	// MessageHeartbeat is sent periodically by the runtime to prove liveness.
	MessageHeartbeat MessageType = "heartbeat"
	// MessageRevoked is sent by the server before closing a connection whose
	// runtime credential has been revoked.
	MessageRevoked MessageType = "revoked"

	// MessageSessionOpen is sent server->runtime to start a new PTY session.
	MessageSessionOpen MessageType = "session.open"
	// MessageSessionReady is sent runtime->server once a PTY session has
	// started successfully.
	MessageSessionReady MessageType = "session.ready"
	// MessageSessionError is sent runtime->server when a session fails to
	// start, or hits a runtime-side error.
	MessageSessionError MessageType = "session.error"
	// MessageSessionResize is sent server->runtime to change a PTY's
	// terminal dimensions.
	MessageSessionResize MessageType = "session.resize"
	// MessageSessionSignal is sent server->runtime to deliver a POSIX signal
	// to a session's process.
	MessageSessionSignal MessageType = "session.signal"
	// MessageSessionClose is sent server->runtime to terminate a session's
	// process and release its PTY.
	MessageSessionClose MessageType = "session.close"
	// MessageSessionClosed is sent runtime->server acknowledging a
	// session.close request.
	MessageSessionClosed MessageType = "session.closed"
	// MessageSessionExit is sent runtime->server when a session's process
	// exits on its own (not via session.close).
	MessageSessionExit MessageType = "session.exit"

	// MessageServiceCommand is sent server->runtime to list/inspect/control
	// a systemd --user service. Unlike session messages, it uses Envelope.ID
	// for request/response correlation (see runtimes.Conn.SendCommand): a
	// single runtime connection may have several service commands in flight
	// at once, with no other natural per-request identifier to key on.
	MessageServiceCommand MessageType = "service.command"
	// MessageServiceResult is sent runtime->server with a successful
	// service.command result, carrying the same Envelope.ID as the request.
	MessageServiceResult MessageType = "service.result"
	// MessageServiceError is sent runtime->server when a service.command
	// fails, carrying the same Envelope.ID as the request.
	MessageServiceError MessageType = "service.error"

	// MessageFileCommand is sent server->runtime to perform a filesystem
	// operation inside a Project's authorized root directory. Like
	// service.command, it uses Envelope.ID for request/response correlation.
	MessageFileCommand MessageType = "file.command"
	// MessageFileResult is sent runtime->server with a successful
	// file.command result, carrying the same Envelope.ID as the request.
	MessageFileResult MessageType = "file.result"
	// MessageFileError is sent runtime->server when a file.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageFileError MessageType = "file.error"

	// MessageDBCommand is sent server->runtime to perform a database
	// operation (Slice 7: connection test only so far). Like
	// service.command, it uses Envelope.ID for request/response correlation.
	MessageDBCommand MessageType = "db.command"
	// MessageDBResult is sent runtime->server with a successful db.command
	// result, carrying the same Envelope.ID as the request.
	MessageDBResult MessageType = "db.result"
	// MessageDBError is sent runtime->server when a db.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageDBError MessageType = "db.error"
)

// Envelope is the outermost shape of every control-plane message. ID is an
// optional correlation identifier for request/response exchanges — used by
// service.command/service.result/service.error (Slice 5); session messages
// (Slice 4) correlate on their payload's own session_id instead, since that
// is already unique per request.
type Envelope struct {
	Version string          `json:"v"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Capabilities reports what the runtime can currently do. Fields stay false
// until the slice implementing that capability lands; advertising true
// without a real implementation would misrepresent the runtime.
type Capabilities struct {
	Terminal   bool     `json:"terminal"`
	Filesystem bool     `json:"filesystem"`
	Services   bool     `json:"services"`
	Databases  []string `json:"databases,omitempty"`
	// OSUsername and HomeDir identify the OS user the runtime process runs
	// as, replacing the old per-Project os_user field: a runtime always runs
	// as one fixed user (ADR-014), so the identity is reported here instead
	// of typed in by a client.
	OSUsername string `json:"os_username"`
	HomeDir    string `json:"home_dir"`
	// Shells is parsed from /etc/shells. Empty when unreadable — no
	// fallback list is offered, matching the Project setup UI's strict
	// dropdown (a client must not be able to type an arbitrary shell path).
	Shells []string `json:"shells,omitempty"`
}

// HelloPayload is the payload of a MessageHello envelope.
type HelloPayload struct {
	Hostname       string       `json:"hostname"`
	OS             string       `json:"os"`
	Arch           string       `json:"arch"`
	RuntimeVersion string       `json:"runtime_version"`
	Capabilities   Capabilities `json:"capabilities"`
}

// SessionOpenPayload is the payload of a MessageSessionOpen envelope. It
// carries everything the runtime needs to start a PTY without trusting any
// value the browser supplied directly: the Control Server resolves these
// from the Project record.
type SessionOpenPayload struct {
	SessionID string            `json:"session_id"`
	Shell     string            `json:"shell"`
	Dir       string            `json:"dir"`
	OSUser    string            `json:"os_user"`
	Env       map[string]string `json:"env,omitempty"`
	Cols      uint16            `json:"cols"`
	Rows      uint16            `json:"rows"`
}

// SessionReadyPayload is the payload of a MessageSessionReady envelope.
type SessionReadyPayload struct {
	SessionID string `json:"session_id"`
}

// SessionErrorPayload is the payload of a MessageSessionError envelope.
type SessionErrorPayload struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
}

// SessionResizePayload is the payload of a MessageSessionResize envelope.
type SessionResizePayload struct {
	SessionID string `json:"session_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// SessionSignalPayload is the payload of a MessageSessionSignal envelope.
// Signal is a POSIX signal name such as "SIGINT" or "SIGTERM".
type SessionSignalPayload struct {
	SessionID string `json:"session_id"`
	Signal    string `json:"signal"`
}

// SessionClosePayload is the payload of a MessageSessionClose envelope.
type SessionClosePayload struct {
	SessionID string `json:"session_id"`
}

// SessionClosedPayload is the payload of a MessageSessionClosed envelope.
type SessionClosedPayload struct {
	SessionID string `json:"session_id"`
}

// SessionExitPayload is the payload of a MessageSessionExit envelope.
type SessionExitPayload struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
}

// ServiceCommandPayload is the payload of a MessageServiceCommand envelope.
type ServiceCommandPayload struct {
	// Action is one of: list, status, start, stop, restart, enable,
	// disable, logs.
	Action string `json:"action"`
	// Unit is the systemd unit name, e.g. "my-app.service". Required for
	// every action except "list".
	Unit string `json:"unit,omitempty"`
	// Lines is the tail length for a "logs" action.
	Lines int `json:"lines,omitempty"`
}

// ServiceInfo describes one systemd --user unit.
type ServiceInfo struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	// Enabled is "enabled", "disabled", "static", or "unknown" if the
	// runtime could not determine it.
	Enabled string `json:"enabled"`
}

// ServiceResultPayload is the payload of a MessageServiceResult envelope.
// Exactly one of Services, Service, or Logs is populated, matching the
// request's Action.
type ServiceResultPayload struct {
	Services []ServiceInfo `json:"services,omitempty"`
	Service  *ServiceInfo  `json:"service,omitempty"`
	Logs     []string      `json:"logs,omitempty"`
}

// ServiceErrorPayload is the payload of a MessageServiceError envelope.
type ServiceErrorPayload struct {
	Error string `json:"error"`
}

// MaxFileBytes caps the size of one file transferred in a file.command
// read/write payload (base64-encoded inside the JSON envelope).
// ponytail: whole-file-in-envelope transfer; if this cap bites real
// projects, add chunked streaming (see docs/PROTOCOL.md Slice 6).
const MaxFileBytes = 24 << 20

// MaxMessageBytes is the WebSocket read limit applied on both ends of the
// control-plane connection. It bounds the largest legal envelope — a
// file.command/file.result carrying MaxFileBytes base64-encoded is ~33 MiB
// after encoding, so the limit is set above that.
const MaxMessageBytes = 48 << 20

// FileCommandPayload is the payload of a MessageFileCommand envelope.
//
// Root is the absolute directory every operation is contained in, resolved
// by the Control Server from the Project record — never taken from a client
// request. Path and To are always relative to Root; the runtime re-validates
// them and re-checks containment via os.Root regardless of what the Control
// Server already checked (docs/SECURITY.md, defense in depth).
type FileCommandPayload struct {
	// Action is one of: list, stat, read, write, create, mkdir, rename,
	// copy, move, delete, search.
	Action string `json:"action"`
	Root   string `json:"root"`
	// Path is the target entry, relative to Root. "." refers to the root
	// itself (used by "list" for directory navigation).
	Path string `json:"path,omitempty"`
	// To is the destination for rename/copy/move, relative to the same Root.
	To string `json:"to,omitempty"`
	// Data carries the file bytes for a "write" action (base64-encoded on
	// the wire by encoding/json).
	Data []byte `json:"data,omitempty"`
	// Query is the name substring for a "search" action.
	Query string `json:"query,omitempty"`
	// DirsOnly restricts a "search" action's matches to directories.
	DirsOnly bool `json:"dirs_only,omitempty"`
}

// FileInfo describes one filesystem entry.
type FileInfo struct {
	Name string `json:"name"`
	// Type is "file", "dir", "symlink", or "other".
	Type string `json:"type"`
	Size int64  `json:"size"`
	// Mode is the POSIX mode string, e.g. "-rw-r--r--".
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mtime"`
}

// FileResultPayload is the payload of a MessageFileResult envelope.
// Exactly one of Entries, Info, Data, or Matches is populated, matching the
// request's Action.
type FileResultPayload struct {
	Entries []FileInfo `json:"entries,omitempty"`
	Info    *FileInfo  `json:"info,omitempty"`
	Data    []byte     `json:"data,omitempty"`
	Matches []string   `json:"matches,omitempty"`
}

// sessionIDFrameLen is the fixed width of the session ID prefix on a binary
// terminal frame. Session IDs are Postgres uuid values rendered as their
// canonical 36-character string form, so the width is constant and no
// length-prefix or delimiter is needed.
const sessionIDFrameLen = 36

// EncodeFrame builds a binary-safe terminal frame: a fixed-width session ID
// prefix followed by raw payload bytes. Terminal input/output must never be
// JSON-escaped (see docs/PROTOCOL.md), so frames travel as WebSocket binary
// messages alongside the JSON envelope used for control messages.
func EncodeFrame(sessionID string, payload []byte) []byte {
	frame := make([]byte, sessionIDFrameLen+len(payload))
	copy(frame, sessionID)
	copy(frame[sessionIDFrameLen:], payload)
	return frame
}

// DBConnectionParams identifies and authenticates a database connection.
// Built server-side from a stored, decrypted database_connections row —
// never taken from a client request (docs/SECURITY.md: database credentials
// are sensitive, and must never be trusted from the browser).
type DBConnectionParams struct {
	// Engine is one of: postgresql, mysql, mssql, sqlite.
	Engine       string `json:"engine"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Database     string `json:"database,omitempty"`
	SSLMode      string `json:"ssl_mode,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
	SearchPath   string `json:"search_path,omitempty"`
	ExtraParams  string `json:"extra_params,omitempty"`
}

// DBCommandPayload is the payload of a MessageDBCommand envelope.
type DBCommandPayload struct {
	// Action is one of: test, invalidate, schemas, tables, columns,
	// indexes, constraints, triggers (Slice 7/8), rows, commit, query
	// (Slice 9). "invalidate" is a fire-and-forget notice (sent via
	// SendEnvelope, no reply) telling the runtime to drop its cached
	// connection pool for ConnectionID — see ADR-021/022.
	Action string `json:"action"`
	// ConnectionID keys the runtime's connection pool cache — a stored
	// connection is opened at most once per runtime process and reused
	// across schema-browsing RPCs, not reopened per request.
	ConnectionID string             `json:"connection_id"`
	Connection   DBConnectionParams `json:"connection"`
	// Schema/Table scope a schemas/tables/columns/indexes/constraints/
	// triggers/rows/commit request. Schema is "" for an engine with no
	// schema concept (SQLite) or a single implicit one (MySQL/MariaDB).
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table,omitempty"`
	// Limit/Offset/Sort/SortDesc/Filter scope a "rows" request. Filter is a
	// raw SQL boolean expression, not a parameterized value — the same
	// deliberate arbitrary-SQL trust model as SQL (ADR-021).
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Sort     string `json:"sort,omitempty"`
	SortDesc bool   `json:"sort_desc,omitempty"`
	Filter   string `json:"filter,omitempty"`
	// Edits carries a "commit" request's staged inserts/updates/deletes.
	Edits []DBRowEdit `json:"edits,omitempty"`
	// SQL carries a "query" request's arbitrary SQL Editor text.
	SQL string `json:"sql,omitempty"`
}

// DBRowEdit is one staged insert/update/delete for a "commit" request.
type DBRowEdit struct {
	// Op is "insert", "update", or "delete".
	Op string `json:"op"`
	// Original identifies the row being updated/deleted — the column
	// values as originally fetched by "rows", used to build a WHERE
	// clause. Unused for insert.
	Original map[string]any `json:"original,omitempty"`
	// Values are the new column values for insert/update. Unused for
	// delete.
	Values map[string]any `json:"values,omitempty"`
}

// DBTableInfo describes one table or view.
type DBTableInfo struct {
	Name string `json:"name"`
	// Kind is "table" or "view".
	Kind string `json:"kind"`
}

// DBColumnInfo describes one table/view column.
type DBColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
	Position int    `json:"position"`
}

// DBIndexInfo describes one table index.
type DBIndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
}

// DBConstraintInfo describes one table constraint.
type DBConstraintInfo struct {
	Name string `json:"name"`
	// Type is "primary_key", "foreign_key", "unique", or "check".
	Type    string   `json:"type"`
	Columns []string `json:"columns"`
	// References is "schema.table(column)" for a foreign_key constraint,
	// otherwise "".
	References string `json:"references,omitempty"`
}

// DBTriggerInfo describes one table trigger. Event/Timing are best-effort —
// not every engine exposes them as cleanly as a name.
type DBTriggerInfo struct {
	Name   string `json:"name"`
	Event  string `json:"event,omitempty"`
	Timing string `json:"timing,omitempty"`
}

// DBRowsResult is the result of a "rows" or "query" request. Exactly one of
// (Columns+Rows) or (IsExec+RowsAffected) is meaningful, matching whether
// the statement produced a result set.
type DBRowsResult struct {
	Columns []string `json:"columns,omitempty"`
	// Rows holds each row's values in Columns order, JSON-marshaled as-is
	// (numbers/strings/bools/null/RFC3339 timestamps; a []byte value that
	// wasn't valid UTF-8 text is base64-encoded by encoding/json).
	Rows [][]any `json:"rows,omitempty"`
	// HasMore is true when more rows exist beyond Limit.
	HasMore      bool  `json:"has_more,omitempty"`
	IsExec       bool  `json:"is_exec,omitempty"`
	RowsAffected int64 `json:"rows_affected,omitempty"`
}

// DBResultPayload is the payload of a MessageDBResult envelope. Exactly one
// of the non-Ok fields is populated, matching the request's Action.
type DBResultPayload struct {
	Ok          bool               `json:"ok,omitempty"`
	Schemas     []string           `json:"schemas,omitempty"`
	Tables      []DBTableInfo      `json:"tables,omitempty"`
	Columns     []DBColumnInfo     `json:"columns,omitempty"`
	Indexes     []DBIndexInfo      `json:"indexes,omitempty"`
	Constraints []DBConstraintInfo `json:"constraints,omitempty"`
	Triggers    []DBTriggerInfo    `json:"triggers,omitempty"`
	Rows        *DBRowsResult      `json:"rows,omitempty"`
}

// DBErrorPayload is the payload of a MessageDBError envelope. Same shape as
// ServiceErrorPayload/FileErrorPayload, kept as its own type for symmetry
// with the other db.* messages.
type DBErrorPayload struct {
	Error string `json:"error"`
}

// DecodeFrame splits a binary terminal frame produced by EncodeFrame back
// into its session ID and payload. ok is false if frame is shorter than the
// session ID prefix.
func DecodeFrame(frame []byte) (sessionID string, payload []byte, ok bool) {
	if len(frame) < sessionIDFrameLen {
		return "", nil, false
	}
	return string(frame[:sessionIDFrameLen]), frame[sessionIDFrameLen:], true
}
