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

// DecodeFrame splits a binary terminal frame produced by EncodeFrame back
// into its session ID and payload. ok is false if frame is shorter than the
// session ID prefix.
func DecodeFrame(frame []byte) (sessionID string, payload []byte, ok bool) {
	if len(frame) < sessionIDFrameLen {
		return "", nil, false
	}
	return string(frame[:sessionIDFrameLen]), frame[sessionIDFrameLen:], true
}
