// Package services manages systemd --user units on the Development Runtime
// (PRD §7): list, status, start, stop, restart, enable, disable, and log
// retrieval, with no root required for normal operation.
package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// unitNamePattern matches a systemd service unit name. Validated as
// defense-in-depth (SECURITY.md): os/exec never invokes a shell, so
// argument injection isn't possible by construction, but this rejects
// nonsensical or unexpected input before it ever reaches systemctl.
// Backslash is legal here (not a shell metacharacter risk, since there's no
// shell) — systemd itself C-style-escapes forbidden bytes in a unit name
// with it (systemd.unit(5)), e.g. "app-at\x2dspi\x2ddbus\x2dbus@autostart.
// service" for a real user-session generated unit; rejecting it would make
// ordinary desktop units unusable through this API.
var unitNamePattern = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]+\.service$`)

// ValidUnitName reports whether name is an acceptable systemd service unit
// name. Exported so the Control Server can reject an obviously-bad unit
// name before dispatching to the runtime.
func ValidUnitName(name string) bool {
	return unitNamePattern.MatchString(name)
}

// Unit describes one systemd --user service.
type Unit struct {
	Name        string
	Description string
	LoadState   string
	ActiveState string
	SubState    string
	// Enabled is "enabled", "disabled", "static", or "unknown".
	Enabled string
}

// Available reports whether a systemd --user instance is reachable for the
// runtime's own OS user. False (rather than an error) when systemctl is
// missing or the user bus can't be reached, so hello.Capabilities.Services
// reflects real, current availability rather than "this build supports it".
func Available(ctx context.Context) bool {
	_, err := exec.CommandContext(ctx, "systemctl", "--user", "list-units", "--no-legend", "--no-pager").Output()
	return err == nil
}

// unitLinePattern parses one line of `systemctl --user list-units`/`--all`
// plain output: UNIT LOAD ACTIVE SUB DESCRIPTION (description may contain
// spaces, so it is captured as the remainder of the line).
var unitLinePattern = regexp.MustCompile(`^\s*(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(.*?)\s*$`)

// unitFileLinePattern parses one line of `systemctl --user list-unit-files`
// plain output: UNIT_FILE STATE PRESET.
var unitFileLinePattern = regexp.MustCompile(`^\s*(\S+)\s+(\S+)\s+\S+\s*$`)

// List returns every service unit systemd --user knows about: both
// currently-loaded units (from list-units --all) and installed-but-not-
// loaded ones (from list-unit-files) — systemd unloads a unit from its live
// table shortly after it stops unless something still references it, so a
// service a user just stopped would otherwise vanish from the list instead
// of staying available to restart (confirmed against a real systemd --user
// instance). The two sources are merged; anything present in list-unit-
// files but not list-units gets a single extra bulk `systemctl show` call
// (not one call per missing unit) to fill in its status.
func List(ctx context.Context) ([]Unit, error) {
	fileStates, err := enabledStates(ctx)
	if err != nil {
		return nil, err
	}

	out, err := exec.CommandContext(ctx, "systemctl", "--user", "list-units",
		"--type=service", "--all", "--no-legend", "--no-pager").Output()
	if err != nil {
		return nil, fmt.Errorf("list-units: %w", exitErr(err))
	}

	units := make(map[string]Unit)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		m := unitLinePattern.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		state, ok := fileStates[m[1]]
		if !ok {
			state = "unknown"
		}
		units[m[1]] = Unit{
			Name:        m[1],
			LoadState:   m[2],
			ActiveState: m[3],
			SubState:    m[4],
			Description: m[5],
			Enabled:     state,
		}
	}

	var missing []string
	for name := range fileStates {
		if _, ok := units[name]; ok {
			continue
		}
		// A bare template unit (e.g. "app@.service", with no instance name)
		// can't be queried or started on its own — only a concrete instance
		// ("app@foo.service") can, and any active instance already has its
		// own entry from list-units. Confirmed against a real systemd
		// --user instance: `systemctl show`/`start` on the bare template
		// itself errors ("is missing the instance name").
		if strings.HasSuffix(name, "@.service") {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing) // deterministic show argument order
		// Best-effort: an unexpected unit name systemctl show rejects must
		// not take down the whole listing over one bad entry — degrade to
		// list-units' loaded set rather than erroring List() entirely.
		if extra, err := bulkStatus(ctx, missing); err == nil {
			for _, u := range extra {
				units[u.Name] = u
			}
		}
	}

	out2 := make([]Unit, 0, len(units))
	for _, u := range units {
		out2 = append(out2, u)
	}
	sort.Slice(out2, func(i, j int) bool { return out2[i].Name < out2[j].Name })
	return out2, nil
}

func enabledStates(ctx context.Context) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "list-unit-files",
		"--type=service", "--no-legend", "--no-pager").Output()
	if err != nil {
		return nil, fmt.Errorf("list-unit-files: %w", exitErr(err))
	}

	states := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		m := unitFileLinePattern.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		states[m[1]] = m[2]
	}
	return states, scanner.Err()
}

// showProperties are the systemctl show properties both Status and
// bulkStatus request.
var showProperties = []string{"Id", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState"}

// Status looks up a single unit's current state.
func Status(ctx context.Context, unit string) (Unit, error) {
	if !ValidUnitName(unit) {
		return Unit{}, fmt.Errorf("invalid unit name %q", unit)
	}
	units, err := bulkStatus(ctx, []string{unit})
	if err != nil {
		return Unit{}, fmt.Errorf("show %s: %w", unit, err)
	}
	if len(units) == 0 {
		return Unit{}, fmt.Errorf("unit %s not found", unit)
	}
	return units[0], nil
}

// bulkStatus runs a single `systemctl show` invocation across all of units
// and parses its blank-line-separated per-unit output blocks. A unit
// systemd reports as not-found (it echoes the request back with
// LoadState=not-found rather than failing) is silently omitted from the
// result rather than erroring the whole batch.
func bulkStatus(ctx context.Context, units []string) ([]Unit, error) {
	if len(units) == 0 {
		return nil, nil
	}
	args := []string{"--user", "show"}
	args = append(args, units...)
	args = append(args, "--no-pager")
	for _, p := range showProperties {
		args = append(args, "-p", p)
	}

	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return nil, exitErr(err)
	}

	var result []Unit
	props := make(map[string]string)
	flush := func() {
		if props["Id"] == "" || props["LoadState"] == "not-found" {
			props = make(map[string]string)
			return
		}
		enabled := props["UnitFileState"]
		if enabled == "" {
			enabled = "unknown"
		}
		result = append(result, Unit{
			Name:        props["Id"],
			Description: props["Description"],
			LoadState:   props["LoadState"],
			ActiveState: props["ActiveState"],
			SubState:    props["SubState"],
			Enabled:     enabled,
		})
		props = make(map[string]string)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	flush() // the last block has no trailing blank line
	return result, scanner.Err()
}

// Start starts unit and returns its resulting status.
func Start(ctx context.Context, unit string) (Unit, error) { return runAction(ctx, "start", unit) }

// Stop stops unit and returns its resulting status.
func Stop(ctx context.Context, unit string) (Unit, error) { return runAction(ctx, "stop", unit) }

// Restart restarts unit and returns its resulting status.
func Restart(ctx context.Context, unit string) (Unit, error) {
	return runAction(ctx, "restart", unit)
}

// Enable enables unit to start at login and returns its resulting status.
func Enable(ctx context.Context, unit string) (Unit, error) { return runAction(ctx, "enable", unit) }

// Disable disables unit and returns its resulting status.
func Disable(ctx context.Context, unit string) (Unit, error) {
	return runAction(ctx, "disable", unit)
}

func runAction(ctx context.Context, action, unit string) (Unit, error) {
	if !ValidUnitName(unit) {
		return Unit{}, fmt.Errorf("invalid unit name %q", unit)
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "--user", action, unit).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return Unit{}, fmt.Errorf("%s %s: %s", action, unit, msg)
	}
	return Status(ctx, unit)
}

// Logs returns the last lines of unit's journal, most recent last. An empty
// (non-nil-error) result means the unit has no logged output — including
// the case where no per-user journal exists on this machine at all, which
// is a normal, non-error outcome of `journalctl --user`.
func Logs(ctx context.Context, unit string, lines int) ([]string, error) {
	if !ValidUnitName(unit) {
		return nil, fmt.Errorf("invalid unit name %q", unit)
	}
	if lines <= 0 {
		lines = 200
	}
	out, err := exec.CommandContext(ctx, "journalctl", "--user", "-u", unit,
		"-n", fmt.Sprint(lines), "--no-pager", "--output=short-iso").Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl %s: %w", unit, exitErr(err))
	}

	var logLines []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == "-- No entries --" {
			continue
		}
		logLines = append(logLines, line)
	}
	return logLines, scanner.Err()
}

// exitErr unwraps an *exec.ExitError's stderr into the error message where
// available, since systemctl/journalctl put the useful diagnostic there.
func exitErr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}
