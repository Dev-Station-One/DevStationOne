// Package agent builds this Development Runtime's identity and drives its
// connection lifecycle.
package agent

import (
	"context"
	"log/slog"
	"os"
	goruntime "runtime"

	"devstation/internal/config"
	"devstation/internal/connection"
	"devstation/internal/protocol"
	"devstation/internal/services"
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
		// Filesystem and database capabilities flip to true once the
		// slices implementing them land. Services reflects real, current
		// availability rather than "this build supports it" — a runtime
		// with no reachable systemd --user instance must not advertise it.
		Capabilities: protocol.Capabilities{
			Terminal: true,
			Services: services.Available(ctx),
		},
	}

	mgr := NewManager(logger)
	return connection.Run(ctx, cfg.ServerURL, cfg.Token, hello, mgr, logger)
}
