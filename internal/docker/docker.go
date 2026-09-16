// Package docker gives the Development Runtime project-scoped Docker
// management (ADR-040): a moby client over the local unix socket, compose
// via the `docker compose` CLI plugin subprocess, and a scope filter that
// only ever surfaces containers belonging to the project's compose stack or
// bind-mounting its working directory.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"devstationone/internal/protocol"
)

// composeFileNames are the compose file names docker compose recognizes,
// tried in its own precedence order.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// composeTimeout bounds one `docker compose up/down` invocation (image
// pulls and builds are slow; everything else reuses the RPC timeout).
const composeUpTimeout = 10 * time.Minute

// composeOutputCap bounds the captured compose CLI output so a chatty build
// cannot balloon a JSON envelope. The tail is what matters after a
// completed run; live progress was rejected in ADR-040.
// ponytail: fixed cap; raise only if real projects need more history.
const composeOutputCap = 64 * 1024

// daemon discovery: standard sockets only (ADR-040 — no config surface).
var socketCandidates = []string{"/var/run/docker.sock", "/run/docker.sock"}

// clientTTL caps how long one dialed moby client is reused. Dialing is
// cheap but API-version negotiation is a round trip; a short-lived cache
// keeps the tab's poll cadence off the wire without a lifecycle to manage.
// ponytail: TTL cache instead of refcounting; a missed refresh costs one dial.
const clientTTL = 30 * time.Second

var (
	clientMu   sync.Mutex
	cachedHost string
	cachedAt   time.Time
	cached     *client.Client
)

// rootlessSocket returns the current user's rootless daemon socket, if its
// directory exists.
func rootlessSocket() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	p := filepath.Join(home, ".docker", "run", "docker.sock")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// Connect exposes the dialed moby client for tests and future callers; it
// is the same cached client dial returns.
func Connect(ctx context.Context) (*client.Client, error) {
	c, _, err := dial(ctx)
	return c, err
}

// dial returns a connected moby client for a reachable local socket,
// reusing a recent one. The returned host is the socket path the client
// was built for.
func dial(ctx context.Context) (*client.Client, string, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if cached != nil && time.Since(cachedAt) < clientTTL {
		return cached, cachedHost, nil
	}
	if cached != nil {
		cached.Close()
		cached = nil
	}

	var host string
	if p := os.Getenv("DOCKER_HOST"); strings.HasPrefix(p, "unix://") {
		host = p
	} else {
		for _, p := range socketCandidates {
			if _, err := os.Stat(p); err == nil {
				host = "unix://" + p
				break
			}
		}
		if host == "" {
			if p, ok := rootlessSocket(); ok {
				host = "unix://" + p
			}
		}
	}
	if host == "" {
		return nil, "", errors.New("no docker socket found")
	}
	c, err := client.New(client.WithHost(host))
	if err != nil {
		return nil, "", err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := c.Ping(pingCtx, client.PingOptions{}); err != nil {
		c.Close()
		return nil, "", fmt.Errorf("docker daemon unreachable: %w", err)
	}
	cached = c
	cachedHost = host
	cachedAt = time.Now()
	return c, host, nil
}

// Discover reports the host's Docker availability for the hello
// capabilities block. Cheap enough to run once per connection.
func Discover(ctx context.Context) protocol.DockerCapability {
	c, _, err := dial(ctx)
	if err != nil {
		return protocol.DockerCapability{}
	}
	cap := protocol.DockerCapability{Available: true}
	if v, err := c.ServerVersion(ctx, client.ServerVersionOptions{}); err == nil {
		cap.Version = v.Version
	}
	cap.Compose = composeAvailable(ctx)
	return cap
}

// composeAvailable checks the `docker compose` CLI plugin.
func composeAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "compose", "version").Run() == nil
}

// ComposeFile returns the compose file in projectDir, or "" when none of
// the standard names exists there. projectDir is the Project's own working
// directory — the caller owns containment; nothing outside it is ever read.
func ComposeFile(projectDir string) string {
	for _, name := range composeFileNames {
		p := filepath.Join(projectDir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// composeProjectName resolves the compose project name the way compose
// itself does: the file's top-level `name:` key when present, else the
// directory's basename. The label check in inScope uses this value, so a
// `name:` override stays visible instead of silently falling back to the
// bind-mount rule.
// ponytail: line scan for one top-level key instead of a YAML dependency;
// quoted scalars handled, block/multiline values are not (compose treats
// name as a simple string) — a full YAML parser is the upgrade path if a
// real project ever needs one.
func composeProjectName(projectDir string) string {
	if file := ComposeFile(projectDir); file != "" {
		if data, err := os.ReadFile(file); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				if !strings.HasPrefix(line, "name:") {
					continue
				}
				v := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
				v = strings.Trim(v, `"'`)
				if v != "" {
					return v
				}
			}
		}
	}
	return filepath.Base(projectDir)
}

// inScope reports whether a container belongs to the project's compose
// stack or bind-mounts the project directory. projectName is the resolved
// compose project name (composeProjectName) — resolved once per listing,
// not per container.
func inScope(s container.Summary, projectName, projectDir string) bool {
	if s.Labels["com.docker.compose.project"] == projectName {
		return true
	}
	for _, m := range s.Mounts {
		if m.Source != "" && (m.Source == projectDir || strings.HasPrefix(m.Source, projectDir+"/")) {
			return true
		}
	}
	return false
}

// List returns the project-scoped containers, sorted by name.
func List(ctx context.Context, projectDir string) ([]protocol.ContainerInfo, error) {
	c, _, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	projectName := composeProjectName(projectDir)
	out := make([]protocol.ContainerInfo, 0, len(res.Items))
	for _, s := range res.Items {
		if !inScope(s, projectName, projectDir) {
			continue
		}
		ci := protocol.ContainerInfo{
			ID:             s.ID,
			Image:          s.Image,
			State:          string(s.State),
			Status:         s.Status,
			ComposeService: s.Labels["com.docker.compose.service"],
			ComposeProject: s.Labels["com.docker.compose.project"],
		}
		if len(s.Names) > 0 {
			ci.Name = strings.TrimPrefix(s.Names[0], "/")
		}
		if s.Health != nil {
			ci.Health = string(s.Health.Status)
		}
		for _, p := range s.Ports {
			if p.PublicPort != 0 {
				ci.Ports = append(ci.Ports, fmt.Sprintf("%v:%d->%d", p.IP, p.PublicPort, p.PrivatePort))
			} else {
				ci.Ports = append(ci.Ports, fmt.Sprintf("%d", p.PrivatePort))
			}
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// scopedID validates that containerID (ID prefix or name) is in project
// scope and returns its full ID. Every action routes through this — the
// runtime re-validates independently of whatever the Control Server
// passed (ADR-040 security rule).
func scopedID(ctx context.Context, projectDir, containerID string) (string, error) {
	list, err := List(ctx, projectDir)
	if err != nil {
		return "", err
	}
	for _, ci := range list {
		if ci.ID == containerID || strings.HasPrefix(ci.ID, containerID) || ci.Name == containerID {
			return ci.ID, nil
		}
	}
	return "", fmt.Errorf("container %q is not in this project's scope", containerID)
}

// Action runs one container lifecycle operation on a scoped container and
// returns the refreshed scoped list.
func Action(ctx context.Context, projectDir, containerID, action string, removeVolumes bool) ([]protocol.ContainerInfo, error) {
	c, _, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	id, err := scopedID(ctx, projectDir, containerID)
	if err != nil {
		return nil, err
	}
	switch action {
	case "start":
		_, err = c.ContainerStart(ctx, id, client.ContainerStartOptions{})
	case "stop":
		_, err = c.ContainerStop(ctx, id, client.ContainerStopOptions{})
	case "restart":
		_, err = c.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	case "remove":
		_, err = c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: removeVolumes, Force: true})
	default:
		return nil, fmt.Errorf("unknown docker action %q", action)
	}
	if err != nil {
		return nil, err
	}
	return List(ctx, projectDir)
}

// Compose runs one `docker compose` action against the project's compose
// file and returns the bounded tail of the CLI's combined output.
func Compose(ctx context.Context, projectDir, action string) (string, error) {
	file := ComposeFile(projectDir)
	if file == "" {
		return "", errors.New("no compose file in the project directory")
	}
	switch action {
	case "up", "down":
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, composeUpTimeout)
		defer cancel()
	case "stop", "restart":
	default:
		return "", fmt.Errorf("unknown compose action %q", action)
	}
	args := []string{"compose", "-f", file, action}
	if action == "up" {
		args = append(args, "-d", "--build")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = projectDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return tail(&buf), fmt.Errorf("docker compose %s: %w: %s", action, err, tail(&buf))
	}
	return tail(&buf), nil
}

// tail returns the last composeOutputCap bytes of buf.
func tail(buf *bytes.Buffer) string {
	b := buf.Bytes()
	if len(b) > composeOutputCap {
		return string(b[len(b)-composeOutputCap:])
	}
	return string(b)
}

// Resources returns read-only images/volumes/networks referenced by the
// project's scoped containers.
func Resources(ctx context.Context, projectDir string) (protocol.DockerResourcesPayload, error) {
	c, _, err := dial(ctx)
	if err != nil {
		return protocol.DockerResourcesPayload{}, err
	}
	res, err := c.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return protocol.DockerResourcesPayload{}, err
	}
	projectName := composeProjectName(projectDir)
	imageIDs := map[string]bool{}
	networks := map[string]bool{}
	volumes := map[string]bool{}
	for _, s := range res.Items {
		if !inScope(s, projectName, projectDir) {
			continue
		}
		imageIDs[s.ImageID] = true
		if s.NetworkSettings != nil {
			for name := range s.NetworkSettings.Networks {
				networks[name] = true
			}
		}
		for _, m := range s.Mounts {
			if m.Type == "volume" && m.Name != "" {
				volumes[m.Name] = true
			}
		}
	}

	out := protocol.DockerResourcesPayload{}

	imgRes, err := c.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return protocol.DockerResourcesPayload{}, err
	}
	for _, img := range imgRes.Items {
		if !imageIDs[img.ID] {
			continue
		}
		tags := img.RepoTags
		if tags == nil {
			tags = []string{}
		}
		out.Images = append(out.Images, protocol.ImageInfo{ID: img.ID, Tags: tags, Size: img.Size, Created: img.Created})
	}
	if out.Images == nil {
		out.Images = []protocol.ImageInfo{}
	}

	volRes, err := c.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return protocol.DockerResourcesPayload{}, err
	}
	// Volume scope: named volumes mounted by an in-scope container (which
	// covers compose-project volumes via the label-based container scope).
	for _, v := range volRes.Items {
		if !volumes[v.Name] {
			continue
		}
		out.Volumes = append(out.Volumes, protocol.VolumeInfo{Name: v.Name, Driver: v.Driver})
	}
	if out.Volumes == nil {
		out.Volumes = []protocol.VolumeInfo{}
	}

	netRes, err := c.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return protocol.DockerResourcesPayload{}, err
	}
	for _, n := range netRes.Items {
		if !networks[n.Name] {
			continue
		}
		out.Networks = append(out.Networks, protocol.NetworkInfo{Name: n.Name, Driver: n.Driver})
	}
	if out.Networks == nil {
		out.Networks = []protocol.NetworkInfo{}
	}
	return out, nil
}
