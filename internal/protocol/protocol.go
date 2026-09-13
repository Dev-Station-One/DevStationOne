// Package protocol defines the versioned control-plane message envelope
// exchanged over the Control Server <-> Development Runtime WebSocket
// connection. Terminal/file/service/database payloads are added in the
// slices that implement them.
package protocol

import "encoding/json"

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
)

// Envelope is the outermost shape of every control-plane message. ID is an
// optional correlation identifier for future request/response exchanges
// (e.g. server-to-runtime commands); it is unused by the messages defined
// in this slice.
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
