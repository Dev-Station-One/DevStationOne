package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"devstationone/internal/database"
	"devstationone/internal/docker"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
)

// dockerDaemonUp skips the test when no reachable daemon exists.
func dockerDaemonUp(t *testing.T) *dockerCtx {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	c, err := docker.Connect(ctx)
	if err != nil {
		t.Skip("no reachable docker daemon; skipping integration test")
	}
	return &dockerCtx{ctx: ctx, c: c}
}

// dockerCtx bundles what the docker API tests need from the daemon.
type dockerCtx struct {
	ctx context.Context
	c   *client.Client
}

// image returns a tiny image guaranteed to have a shell: busybox, pulled
// once if missing. Test-only.
func (dc *dockerCtx) image(t *testing.T) string {
	t.Helper()
	const ref = "busybox:latest"
	if _, err := dc.c.ImageInspect(dc.ctx, ref); err == nil {
		return ref
	}
	pull, err := dc.c.ImagePull(dc.ctx, ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	_, _ = io.Copy(io.Discard, pull)
	_ = pull.Close()
	return ref
}

// create starts one disposable container and returns its ID.
func (dc *dockerCtx) create(t *testing.T, labels map[string]string, cmd []string) string {
	t.Helper()
	if cmd == nil {
		cmd = []string{"/bin/sh", "-c", "sleep 30"}
	}
	res, err := dc.c.ContainerCreate(dc.ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: dc.image(t), Cmd: cmd, Labels: labels},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if _, err := dc.c.ContainerStart(dc.ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		dc.remove(res.ID)
		t.Fatalf("start container: %v", err)
	}
	return res.ID
}

// remove force-removes a disposable container, ignoring errors.
func (dc *dockerCtx) remove(id string) {
	_, _ = dc.c.ContainerRemove(dc.ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
}

// TestDockerHTTP_StatusShape covers the Docker tab's first-render endpoint
// against the real runtime dispatcher (real daemon behind it).
func TestDockerHTTP_StatusShape(t *testing.T) {
	dockerDaemonUp(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/docker", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var st protocol.DockerStatusPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Available {
		t.Fatalf("available = false with a reachable daemon: %+v", st)
	}
	if st.Containers == nil {
		t.Fatal("containers array must never be null")
	}
	if st.Version == "" {
		t.Fatal("empty daemon version with a reachable daemon")
	}
}

// TestDockerHTTP_BadRequests covers path-value validation before anything
// reaches the runtime: bad container ids, unknown actions.
func TestDockerHTTP_BadRequests(t *testing.T) {
	dockerDaemonUp(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/v1/projects/" + projectID + "/docker/containers/bad%2Fid/start", http.StatusBadRequest},
		{http.MethodPost, "/api/v1/projects/" + projectID + "/docker/containers/0123456789abcdef/explode", http.StatusNotFound},
		{http.MethodPost, "/api/v1/projects/" + projectID + "/docker/compose/explode", http.StatusNotFound},
	}
	for _, tt := range cases {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Fatalf("%s %s: code = %d, want %d, body = %s", tt.method, tt.path, rec.Code, tt.want, rec.Body.String())
		}
	}
}

// TestDockerHTTP_ComposeWithoutFile checks that a project with no compose
// file answers the compose action with the runtime's error.
func TestDockerHTTP_ComposeWithoutFile(t *testing.T) {
	dockerDaemonUp(t)
	router, _, cookie, projectID, _ := sessionsTestSetup(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/docker/compose/down", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("compose/down without compose file: code = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
}

// TestDockerHTTP_LifecycleAndScope runs a real disposable container through
// the HTTP API: it must appear in the scoped list, stop, and disappear on
// remove; a decoy container must never appear.
func TestDockerHTTP_LifecycleAndScope(t *testing.T) {
	dc := dockerDaemonUp(t)
	router, _, cookie, projectID, db := sessionsTestSetup(t)
	projectDir := projectDirFromTestDB(t, db, projectID)

	inID := dc.create(t, map[string]string{"com.docker.compose.project": filepath.Base(projectDir)}, nil)
	defer dc.remove(inID)
	decoyID := dc.create(t, nil, nil)
	defer dc.remove(decoyID)

	// list: in-scope present, decoy absent
	st := dockerGet(t, router, cookie, projectID)
	var inFound, decoyFound bool
	for _, ci := range st.Containers {
		if ci.ID == inID {
			inFound = true
		}
		if ci.ID == decoyID {
			decoyFound = true
		}
	}
	if !inFound || decoyFound {
		t.Fatalf("scope leak: inFound=%v decoyFound=%v list=%v", inFound, decoyFound, st.Containers)
	}

	// stop via the API, then verify state
	dockerAction(t, router, cookie, projectID, inID, "stop")
	st = dockerGet(t, router, cookie, projectID)
	for _, ci := range st.Containers {
		if ci.ID == inID && ci.State != "exited" {
			t.Fatalf("after stop: state = %q, want exited", ci.State)
		}
	}

	// remove via the API, then verify absence
	dockerAction(t, router, cookie, projectID, inID, "remove")
	st = dockerGet(t, router, cookie, projectID)
	for _, ci := range st.Containers {
		if ci.ID == inID {
			t.Fatal("removed container still listed")
		}
	}
}

// TestDockerHTTP_LogsStream follows a real container's logs over the WS
// endpoint: the probe lines arrive in streamed batches.
func TestDockerHTTP_LogsStream(t *testing.T) {
	dc := dockerDaemonUp(t)
	router, server, cookie, projectID, db := sessionsTestSetup(t)
	projectDir := projectDirFromTestDB(t, db, projectID)

	// A container that prints a distinct line forever.
	id := dc.create(t, map[string]string{"com.docker.compose.project": filepath.Base(projectDir)},
		[]string{"/bin/sh", "-c", "i=0; while true; do echo dslogprobe-$i; i=$((i+1)); sleep 0.3; done"})
	defer dc.remove(id)

	// Wait for the container to actually run before opening the stream.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := dockerGet(t, router, cookie, projectID)
		for _, ci := range st.Containers {
			if ci.ID == id && ci.State == "running" {
				goto running
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("probe container never reached running state")
running:

	// Connect to the logs stream. The handler upgrades immediately, so the
	// request must go through the real HTTP server, not httptest.NewRecorder.
	wsURL := "ws" + server.URL[len("http"):] + "/api/v1/projects/" + projectID + "/docker/containers/" + id + "/logs"
	ws, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}},
	})
	if err != nil {
		t.Fatalf("dial logs stream: %v", err)
	}
	defer ws.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sawProbe, batches := false, 0
	for !sawProbe && batches < 30 {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("read batch %d: %v", batches, err)
		}
		batches++
		var p protocol.DockerLogsPayload
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		for _, line := range p.Lines {
			if len(line) > 8 && line[:8] == "dslogpro" {
				sawProbe = true
			}
		}
	}
	if !sawProbe {
		t.Fatalf("log stream carried no probe line in %d batches", batches)
	}
}

// --- helpers ---

func dockerGet(t *testing.T, router http.Handler, cookie *http.Cookie, projectID string) protocol.DockerStatusPayload {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/docker", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("docker status: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var st protocol.DockerStatusPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return st
}

func dockerAction(t *testing.T, router http.Handler, cookie *http.Cookie, projectID, containerID, action string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/docker/containers/"+containerID+"/"+action, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: code = %d, body = %s", action, containerID, rec.Code, rec.Body.String())
	}
}

// projectDirFromTestDB re-reads the project's working directory (the scope
// root the runtime commands were built with). The compose-project label the
// tests stamp onto containers is derived from it, so it must match.
func projectDirFromTestDB(t *testing.T, db *database.DB, projectID string) string {
	t.Helper()
	p, err := projects.Get(context.Background(), db.Pool, projectID)
	if err != nil {
		t.Fatalf("read project %s: %v", projectID, err)
	}
	return p.WorkingDirectory
}
