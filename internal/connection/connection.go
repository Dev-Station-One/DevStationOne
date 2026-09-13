// Package connection maintains the Development Runtime's outbound
// authenticated WebSocket connection to the Control Server, including
// reconnect-with-backoff and periodic heartbeats.
package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"devstation/internal/protocol"
)

const (
	heartbeatInterval = 15 * time.Second
	minBackoff        = 1 * time.Second
	maxBackoff        = 30 * time.Second
)

// Run dials the Control Server and maintains the connection, reconnecting
// with exponential backoff on failure, until ctx is cancelled.
func Run(ctx context.Context, serverURL, token string, hello protocol.HelloPayload, logger *slog.Logger) error {
	dialURL, err := connectURL(serverURL)
	if err != nil {
		return err
	}

	backoff := minBackoff
	for ctx.Err() == nil {
		connected, err := connectOnce(ctx, dialURL, token, hello, logger)
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
func connectOnce(ctx context.Context, dialURL, token string, hello protocol.HelloPayload, logger *slog.Logger) (bool, error) {
	conn, _, err := websocket.Dial(ctx, dialURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	if err := sendEnvelope(ctx, conn, protocol.MessageHello, hello); err != nil {
		return false, fmt.Errorf("send hello: %w", err)
	}
	logger.Info("connected to control server")

	return true, heartbeatLoop(ctx, conn, logger)
}

func sendEnvelope(ctx context.Context, conn *websocket.Conn, msgType protocol.MessageType, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: msgType, Payload: body})
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// heartbeatLoop sends periodic heartbeats and watches for server-initiated
// messages (such as revocation) until the connection fails or ctx is done.
func heartbeatLoop(ctx context.Context, conn *websocket.Conn, logger *slog.Logger) error {
	readErrCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				readErrCh <- err
				return
			}
			var env protocol.Envelope
			if json.Unmarshal(data, &env) == nil && env.Type == protocol.MessageRevoked {
				logger.Warn("runtime credential revoked by control server")
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
			if err := sendEnvelope(ctx, conn, protocol.MessageHeartbeat, struct{}{}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}
