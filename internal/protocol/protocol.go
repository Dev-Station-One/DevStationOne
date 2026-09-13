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
