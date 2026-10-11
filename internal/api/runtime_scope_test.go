package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"devstationone/internal/auth"
	"devstationone/internal/editor"
	"devstationone/internal/events"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// A runtime must not be able to end or otherwise act on another runtime's
// sessions, whatever session_id it puts in a message.
func TestRuntimeEnvelope_IgnoresForeignSession(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	user, err := auth.CreateUser(ctx, db.Pool, "scope@example.com", "password123", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	rtA, _, err := runtimes.Create(ctx, db.Pool, "rt-a", user.ID)
	if err != nil {
		t.Fatalf("create runtime a: %v", err)
	}
	rtB, _, err := runtimes.Create(ctx, db.Pool, "rt-b", user.ID)
	if err != nil {
		t.Fatalf("create runtime b: %v", err)
	}
	proj, err := projects.Create(ctx, db.Pool, projects.Fields{Name: "p", RuntimeID: rtA.ID, WorkingDirectory: "/tmp", Shell: "/bin/bash"}, user.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	sess, err := sessions.Create(ctx, db.Pool, proj.ID, "s", 80, 24, user.ID, "xterm")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	hub := sessions.NewHub()
	exit, _ := json.Marshal(protocol.SessionExitPayload{SessionID: sess.ID, ExitCode: 0})
	env := protocol.Envelope{Version: protocol.Version, Type: protocol.MessageSessionExit, Payload: exit}
	registry := runtimes.NewRegistry()
	bus := events.NewBus()

	// Runtime B claims A's session exited: ignored.
	handleRuntimeEnvelope(ctx, env, db, registry, hub, bus, editor.NewHub(), newDockerLogsHub(), logger, rtB.ID)
	got, err := sessions.Get(ctx, db.Pool, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Status != sessions.StatusRequested {
		t.Fatalf("status after foreign exit = %q, want %q", got.Status, sessions.StatusRequested)
	}

	// The owning runtime A is honored.
	handleRuntimeEnvelope(ctx, env, db, registry, hub, bus, editor.NewHub(), newDockerLogsHub(), logger, rtA.ID)
	got, _ = sessions.Get(ctx, db.Pool, sess.ID)
	if got.Status != sessions.StatusExited {
		t.Fatalf("status after owner exit = %q, want %q", got.Status, sessions.StatusExited)
	}

	if sessions.BelongsToRuntime(ctx, db.Pool, "not-a-uuid", rtA.ID) {
		t.Fatal("malformed session id reported as owned")
	}
}
