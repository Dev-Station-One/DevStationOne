// Package agent builds this Development Runtime's identity and drives its
// connection lifecycle.
package agent

import (
	"context"
	"log/slog"
	"os"
	"os/user"
	goruntime "runtime"
	"strings"

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

	var osUsername, homeDir string
	if u, err := user.Current(); err != nil {
		logger.Warn("determine runtime OS user failed", "error", err)
	} else {
		osUsername, homeDir = u.Username, u.HomeDir
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
			OSUsername: osUsername,
			HomeDir:    homeDir,
			Shells:     readShells("/etc/shells"),
		},
	}

	mgr := NewManager(logger)
	return connection.Run(ctx, cfg.ServerURL, cfg.Token, hello, mgr, logger)
}

// readShells returns the login shells listed in path (normally /etc/shells),
// or nil if it can't be read. No fallback list: the Project setup UI treats
// an empty result as "no shell choices yet" rather than guessing.
func readShells(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseShells(data)
}

func parseShells(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
