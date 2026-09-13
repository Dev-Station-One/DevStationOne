// Package agent builds this Development Runtime's identity and drives its
// connection lifecycle.
package agent

import (
	"context"
	"log/slog"
	"os"
	goruntime "runtime"

	"devstationone/internal/config"
	"devstationone/internal/connection"
	"devstationone/internal/protocol"
	"devstationone/internal/services"
)

// Version is the Development Runtime build version advertised to the
// Control Server during enrollment.
const Version = "0.1.0"

// Run builds this runtime's hello payload and maintains its connection to
// the Control Server until ctx is cancelled.
func Run(ctx context.Context, cfg config.Runtime, logger *slog.Logger) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	hello := protocol.HelloPayload{
		Hostname:       hostname,
		OS:             goruntime.GOOS,
		Arch:           goruntime.GOARCH,
		RuntimeVersion: Version,
		// Services reflects real, current availability rather than "this
		// build supports it" — a runtime with no reachable systemd --user
		// instance must not advertise it. Filesystem is always available in
		// a build that contains it. Database capability flips to true once
		// the slices implementing it land.
		Capabilities: protocol.Capabilities{
			Terminal:   true,
			Filesystem: true,
			Services:   services.Available(ctx),
		},
	}

	mgr := NewManager(logger)
	return connection.Run(ctx, cfg.ServerURL, cfg.Token, hello, mgr, logger)
}
