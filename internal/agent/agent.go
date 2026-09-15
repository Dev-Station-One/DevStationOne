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
	"devstationone/internal/dbconnectors"
	"devstationone/internal/docker"
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
		// instance must not advertise it. Filesystem and the database
		// drivers are always available in a build that contains them.
		Capabilities: protocol.Capabilities{
			Terminal:   true,
			Filesystem: true,
			Services:   services.Available(ctx),
			Databases:  dbconnectors.SupportedEngines(),
			OSUsername: osUsername,
			HomeDir:    homeDir,
			Shells:     readShells("/etc/shells"),
			Users:      readColonNames("/etc/passwd"),
			Groups:     readColonNames("/etc/group"),
			// Docker reflects a reachable daemon and installed compose
			// plugin, same "report reality" rule as Services (ADR-040).
			Docker: func() *protocol.DockerCapability {
				cap := docker.Discover(ctx)
				if !cap.Available {
					return nil
				}
				return &cap
			}(),
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

// readColonNames returns the first colon-separated field of every
// non-comment, non-blank line in an /etc/passwd- or /etc/group-shaped file —
// the account or group name; both files share that column-zero shape. nil
// if path can't be read, matching readShells: no fallback, no filtering by
// uid/gid range — the file properties owner/group dropdowns show exactly
// what the runtime host reports, system accounts included.
func readColonNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseColonNames(data)
}

func parseColonNames(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}
