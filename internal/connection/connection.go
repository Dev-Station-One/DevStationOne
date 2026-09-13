// Package connection maintains the Development Runtime's outbound
// authenticated WebSocket connection to the Control Server, including
// reconnect-with-backoff, periodic heartbeats, and dispatch of control
// messages and binary terminal frames to a Manager.
package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/protocol"
)

const (
	heartbeatInterval = 15 * time.Second
	minBackoff        = 1 * time.Second
	maxBackoff        = 30 * time.Second
)

// Sender lets a Manager push control messages and terminal byte frames back
// to the Control Server over the currently active connection.
type Sender interface {
	SendEnvelope(msgType protocol.MessageType, payload any) error
	SendReply(id string, msgType protocol.MessageType, payload any) error
	SendFrame(sessionID string, data []byte) error
}

// Manager reacts to control-plane messages and terminal frames received
// from the Control Server. SetSender is called with the active Sender each
// time a connection is established, and with nil when it drops — PTYs
// started on a prior connection keep running across a reconnect, so the
// Manager must outlive any single connection attempt.
type Manager interface {
	SetSender(s Sender)
	HandleEnvelope(env protocol.Envelope)
	HandleFrame(sessionID string, data []byte)
}

// Run dials the Control Server and maintains the connection, reconnecting
// with exponential backoff on failure, until ctx is cancelled.
func Run(ctx context.Context, serverURL, token string, hello protocol.HelloPayload, mgr Manager, logger *slog.Logger) error {
	dialURL, err := connectURL(serverURL)
	if err != nil {
		return err
	}

	backoff := minBackoff
	for ctx.Err() == nil {
		connected, err := connectOnce(ctx, dialURL, token, hello, mgr, logger)
		if ctx.Err() != nil {
			return nil
		}
		if connected {
			backoff = minBackoff
		}
		logger.Warn("runtime connection ended, retrying", "error", err, "retry_in", backoff)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil
}

// connectURL derives the runtime WebSocket endpoint from the configured
// Control Server URL (which may be http(s):// or already ws(s)://).
func connectURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/runtimes/connect"
	return u.String(), nil
}

// connectOnce performs a single connection attempt. The returned bool
// reports whether the handshake succeeded (used by Run to decide whether to
// reset its backoff), independent of the returned error.
func connectOnce(ctx context.Context, dialURL, token string, hello protocol.HelloPayload, mgr Manager, logger *slog.Logger) (bool, error) {
	conn, _, err := websocket.Dial(ctx, dialURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	// The runtime reads the largest legal control-plane envelopes, including
	// file.command payloads carrying up to protocol.MaxFileBytes.
	// coder/websocket's own default is far smaller (~32 KiB), which would
	// reject large file transfers.
	conn.SetReadLimit(protocol.MaxMessageBytes)
	defer conn.CloseNow()

	sender := &wsSender{conn: conn}
	if err := sender.SendEnvelope(protocol.MessageHello, hello); err != nil {
		return false, fmt.Errorf("send hello: %w", err)
	}
	logger.Info("connected to control server")

	mgr.SetSender(sender)
	defer mgr.SetSender(nil)

	return true, runLoop(ctx, conn, sender, mgr)
}

// wsSender adapts a *websocket.Conn to Sender, serializing writes: the
// heartbeat ticker and every PTY's output goroutine share one connection.
type wsSender struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (s *wsSender) SendEnvelope(msgType protocol.MessageType, payload any) error {
	return s.sendEnvelope("", msgType, payload)
}

// SendReply sends msgType carrying id as its correlation ID, completing a
// server-initiated request/response exchange (see runtimes.Conn.SendCommand
// on the Control Server side).
func (s *wsSender) SendReply(id string, msgType protocol.MessageType, payload any) error {
	return s.sendEnvelope(id, msgType, payload)
}

func (s *wsSender) sendEnvelope(id string, msgType protocol.MessageType, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: msgType, ID: id, Payload: body})
	if err != nil {
		return err
	}
	return s.write(websocket.MessageText, data)
}

func (s *wsSender) SendFrame(sessionID string, payload []byte) error {
	return s.write(websocket.MessageBinary, protocol.EncodeFrame(sessionID, payload))
}

func (s *wsSender) write(msgType websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Write(ctx, msgType, data)
}

// runLoop reads frames/envelopes and dispatches them to mgr, sending
// periodic heartbeats, until the connection fails or ctx is done.
func runLoop(ctx context.Context, conn *websocket.Conn, sender *wsSender, mgr Manager) error {
	readErrCh := make(chan error, 1)
	go func() {
		for {
			msgType, data, err := conn.Read(ctx)
			if err != nil {
				readErrCh <- err
				return
			}
			if msgType == websocket.MessageBinary {
				if sessionID, payload, ok := protocol.DecodeFrame(data); ok {
					mgr.HandleFrame(sessionID, payload)
				}
				continue
			}
			var env protocol.Envelope
			if json.Unmarshal(data, &env) == nil {
				mgr.HandleEnvelope(env)
			}
		}
	}()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "shutting down")
			return nil
		case err := <-readErrCh:
			return err
		case <-ticker.C:
			if err := sender.SendEnvelope(protocol.MessageHeartbeat, struct{}{}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}
