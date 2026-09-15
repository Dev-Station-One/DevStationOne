package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// testUnit installs a small, disposable transient-looking unit file under
// the real user's systemd --user directory, runs daemon-reload, and
// guarantees cleanup (stop, disable, remove file, daemon-reload) even if
// the test fails. It never touches any pre-existing unit — the name is
// randomized per test run.
func testUnit(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if !Available(ctx) {
		t.Skip("systemd --user not available; skipping integration test")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("determine home dir: %v", err)
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	name := fmt.Sprintf("devstationone-test-%d.service", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	unitFile := "[Unit]\nDescription=DevStationOne Slice 5 test service\n\n" +
		"[Service]\nExecStart=/bin/sh -c 'while true; do sleep 1; done'\n\n" +
		"[Install]\nWantedBy=default.target\n"
	if err := os.WriteFile(path, []byte(unitFile), 0o644); err != nil {
		t.Fatalf("write unit file: %v", err)
	}
	if err := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run(); err != nil {
		t.Fatalf("daemon-reload: %v", err)
	}

	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "stop", name).Run()
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "disable", name).Run()
		_ = os.Remove(path)
		_ = exec.CommandContext(context.Background(), "systemctl", "--user", "daemon-reload").Run()
	})

	return name
}

func TestServiceLifecycle_RealSystemdUser(t *testing.T) {
	ctx := context.Background()
	unit := testUnit(t)

	// start
	st, err := Start(ctx, unit)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.ActiveState != "active" || st.SubState != "running" {
		t.Fatalf("after Start: %+v, want active/running", st)
	}

	// status
	st, err = Status(ctx, unit)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Name != unit {
		t.Fatalf("Status name = %q, want %q", st.Name, unit)
	}

	// list includes it, active/running
	units, err := List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, u := range units {
		if u.Name == unit {
			found = true
			if u.ActiveState != "active" {
				t.Fatalf("List entry for %s: ActiveState = %q, want active", unit, u.ActiveState)
			}
		}
	}
	if !found {
		t.Fatalf("List did not include %s", unit)
	}

	// restart
	st, err = Restart(ctx, unit)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if st.ActiveState != "active" {
		t.Fatalf("after Restart: %+v, want active", st)
	}

	// enable / disable
	st, err = Enable(ctx, unit)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if st.Enabled != "enabled" {
		t.Fatalf("after Enable: Enabled = %q, want enabled", st.Enabled)
	}
	st, err = Disable(ctx, unit)
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if st.Enabled == "enabled" {
		t.Fatalf("after Disable: Enabled = %q, want not enabled", st.Enabled)
	}

	// stop
	st, err = Stop(ctx, unit)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st.ActiveState == "active" {
		t.Fatalf("after Stop: ActiveState = %q, want inactive", st.ActiveState)
	}

	// A stopped unit systemd has since garbage-collected from its live
	// unit table must still appear in List (found by cross-referencing
	// list-unit-files) — verified against a real systemd --user instance,
	// where this unit disappears from `systemctl --user list-units --all`
	// within about half a second of stopping.
	units, err = List(ctx)
	if err != nil {
		t.Fatalf("List after stop: %v", err)
	}
	found = false
	for _, u := range units {
		if u.Name == unit {
			found = true
			if u.ActiveState == "active" {
				t.Fatalf("List entry for stopped %s: ActiveState = %q, want inactive", unit, u.ActiveState)
			}
		}
	}
	if !found {
		t.Fatalf("List dropped %s after it stopped (and was GC'd from list-units)", unit)
	}

	// logs: a real, well-formed result (possibly empty on a machine with no
	// per-user journal namespace — see Logs' doc comment).
	if _, err := Logs(ctx, unit, 50); err != nil {
		t.Fatalf("Logs: %v", err)
	}
}

func TestStatus_UnknownUnit(t *testing.T) {
	ctx := context.Background()
	if !Available(ctx) {
		t.Skip("systemd --user not available; skipping integration test")
	}
	if _, err := Status(ctx, "devstationone-definitely-not-a-real-unit.service"); err == nil {
		t.Fatal("expected an error for a nonexistent unit")
	}
}

func TestValidUnitName(t *testing.T) {
	valid := []string{
		"dbus.service", "my-app.service", "app@instance.service", "a_b.c.service",
		// A real systemd --user generated unit, C-style-escaped by systemd
		// itself (systemd.unit(5)) — must be accepted, or ordinary desktop
		// session units become unusable through this API.
		`app-at\x2dspi\x2ddbus\x2dbus@autostart.service`,
	}
	for _, name := range valid {
		if !ValidUnitName(name) {
			t.Errorf("ValidUnitName(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"dbus", "dbus.socket", "../etc/passwd.service", "dbus; rm -rf /.service",
		"dbus.service; rm -rf /", "", "dbus service.service",
	}
	for _, name := range invalid {
		if ValidUnitName(name) {
			t.Errorf("ValidUnitName(%q) = true, want false", name)
		}
	}
}
