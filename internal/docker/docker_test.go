package docker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// TestComposeFile_Detection covers the four-name precedence and the
// no-file case.
func TestComposeFile_Detection(t *testing.T) {
	dir := t.TempDir()
	if got := ComposeFile(dir); got != "" {
		t.Fatalf("empty dir: ComposeFile = %q, want \"\"", got)
	}

	// A directory named like a compose file must not win (not regular).
	sub := filepath.Join(dir, "compose.yaml")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ComposeFile(dir); got != "" {
		t.Fatalf("dir named compose.yaml: ComposeFile = %q, want \"\"", got)
	}

	// Each name in isolation is found.
	for _, name := range composeFileNames {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ComposeFile(dir); filepath.Base(got) != name {
			t.Fatalf("ComposeFile(%q) = %q, want %q", name, got, name)
		}
	}

	// Precedence: compose.yaml wins over docker-compose.yml.
	dir2 := t.TempDir()
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		if err := os.WriteFile(filepath.Join(dir2, name), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := ComposeFile(dir2); filepath.Base(got) != "compose.yaml" {
		t.Fatalf("precedence: got %q, want compose.yaml", got)
	}
}

// TestInScope covers the compose-label and bind-mount rules, including the
// decoys that must never pass.
func TestInScope(t *testing.T) {
	projectDir := "/home/dev/myproject"

	tests := []struct {
		name string
		s    container.Summary
		want bool
	}{
		{"compose label match", container.Summary{Labels: map[string]string{"com.docker.compose.project": "myproject"}}, true},
		{"other compose project", container.Summary{Labels: map[string]string{"com.docker.compose.project": "other"}}, false},
		{"no labels", container.Summary{}, false},
		{"bind mount project dir itself", container.Summary{Mounts: []container.MountPoint{{Source: projectDir}}}, true},
		{"bind mount under project dir", container.Summary{Mounts: []container.MountPoint{{Source: projectDir + "/data"}}}, true},
		{"bind mount sibling dir", container.Summary{Mounts: []container.MountPoint{{Source: "/home/dev/myproject-other"}}}, false},
		{"bind mount elsewhere", container.Summary{Mounts: []container.MountPoint{{Source: "/var/lib/postgres"}}}, false},
		{"volume with no source", container.Summary{Mounts: []container.MountPoint{{Name: "data", Type: "volume"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inScope(tt.s, projectDir); got != tt.want {
				t.Fatalf("inScope = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestList_ScopingAndProjection runs a real container against the real
// daemon (skipped when unavailable) and checks that only it — and not a
// decoy from another scope — comes back, with the projection fields intact.
func TestList_ScopingAndProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := dial(ctx); err != nil {
		t.Skip("no reachable docker daemon; skipping integration test")
	}

	projectDir := t.TempDir()
	base := filepath.Base(projectDir)

	c, _, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// In-scope container: mounts the project directory.
	inID, err := runContainer(ctx, c, testImage(ctx, c), "sleep 30",
		map[string]string{"com.docker.compose.project": base},
		[]string{projectDir + "/data:/data"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeContainer(ctx, c, inID)

	// Decoy container: no project label, no project mounts.
	decoyID, err := runContainer(ctx, c, testImage(ctx, c), "sleep 30", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeContainer(ctx, c, decoyID)

	list, err := List(ctx, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	var inFound, decoyFound bool
	for _, ci := range list {
		if ci.ID == inID {
			inFound = true
			if ci.ComposeProject != base {
				t.Fatalf("in-scope container ComposeProject = %q, want %q", ci.ComposeProject, base)
			}
			if ci.State != "running" {
				t.Fatalf("in-scope container State = %q, want running", ci.State)
			}
		}
		if ci.ID == decoyID {
			decoyFound = true
		}
	}
	if !inFound {
		t.Fatalf("scoped list %v does not contain the in-scope container %s", list, inID)
	}
	if decoyFound {
		t.Fatalf("scoped list contains the out-of-scope decoy %s", decoyID)
	}

	// scopedID accepts a prefix and rejects an out-of-scope ID.
	full, err := scopedID(ctx, projectDir, inID[:12])
	if err != nil || full != inID {
		t.Fatalf("scopedID(prefix) = %q, %v; want %q, nil", full, err, inID)
	}
	if _, err := scopedID(ctx, projectDir, decoyID); err == nil {
		t.Fatal("scopedID accepted an out-of-scope container")
	}
}

// TestResources_ScopeOnly checks that the read-only resource lists never
// contain resources the decoy references (real daemon; skipped when absent).
func TestResources_ScopeOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := dial(ctx); err != nil {
		t.Skip("no reachable docker daemon; skipping integration test")
	}

	projectDir := t.TempDir()
	base := filepath.Base(projectDir)

	c, _, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inID, err := runContainer(ctx, c, testImage(ctx, c), "sleep 30",
		map[string]string{"com.docker.compose.project": base}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeContainer(ctx, c, inID)

	res, err := Resources(ctx, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) == 0 {
		t.Fatal("no images returned for an in-scope container")
	}
	if len(res.Networks) == 0 {
		t.Fatal("no networks returned for an in-scope container")
	}
}

// testImage returns a tiny image guaranteed to have a shell: busybox,
// pulled once if missing. Test-only.
func testImage(ctx context.Context, c *client.Client) string {
	const ref = "busybox:latest"
	if _, err := c.ImageInspect(ctx, ref); err == nil {
		return ref
	}
	pull, err := c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return ref // Let the create below report the real failure.
	}
	_, _ = io.Copy(io.Discard, pull)
	_ = pull.Close()
	return ref
}

// runContainer creates and starts one disposable container and returns its
// ID.
func runContainer(ctx context.Context, c *client.Client, image, cmd string, labels map[string]string, binds []string) (string, error) {
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  image,
			Cmd:    []string{"/bin/sh", "-c", cmd},
			Labels: labels,
		},
		HostConfig: &container.HostConfig{Binds: binds},
	})
	if err != nil {
		return "", err
	}
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		removeContainer(ctx, c, res.ID)
		return "", err
	}
	return res.ID, nil
}

// removeContainer force-removes a disposable container, ignoring errors.
func removeContainer(ctx context.Context, c *client.Client, id string) {
	_, _ = c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
}
