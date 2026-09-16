package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devstationone/internal/protocol"
)

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeEditSender records replies (SendReply) and pushes (SendEnvelope) so
// tests can observe the edit manager's replies and code.file.changed pushes.
type fakeEditSender struct {
	replies   chan protocol.Envelope
	pushes    chan protocol.CodeFileChangedPayload
	heartbeat chan struct{}
}

func newFakeEditSender() *fakeEditSender {
	return &fakeEditSender{
		replies:   make(chan protocol.Envelope, 16),
		pushes:    make(chan protocol.CodeFileChangedPayload, 16),
		heartbeat: make(chan struct{}, 1),
	}
}

func (s *fakeEditSender) SendEnvelope(msgType protocol.MessageType, payload any) error {
	if msgType != protocol.MessageCodeFileChanged {
		return nil
	}
	body, _ := json.Marshal(payload)
	var p protocol.CodeFileChangedPayload
	_ = json.Unmarshal(body, &p)
	s.pushes <- p
	return nil
}

func (s *fakeEditSender) SendReply(id string, msgType protocol.MessageType, payload any) error {
	body, _ := json.Marshal(payload)
	s.replies <- protocol.Envelope{Type: msgType, Payload: body}
	return nil
}

func (s *fakeEditSender) SendFrame(string, []byte) error { return nil }

func (s *fakeEditSender) waitPush(t *testing.T) protocol.CodeFileChangedPayload {
	t.Helper()
	select {
	case p := <-s.pushes:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for code.file.changed push")
		return protocol.CodeFileChangedPayload{}
	}
}

func writeEditFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEditOpen(t *testing.T) {
	root := t.TempDir()
	writeEditFile(t, root, "main.go", "hello world")

	m := newEditManager(testLogger(t))
	res, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "main.go"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(res.Data) != "hello world" || res.Binary || res.Size != 11 {
		t.Errorf("open result = %+v", res)
	}
	if res.MTime.IsZero() {
		t.Error("mtime should be populated")
	}

	// Binary refusal.
	writeEditFile(t, root, "blob.bin", "ok\x00not-text")
	res, err = m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "blob.bin"})
	if err != nil {
		t.Fatalf("open binary: %v", err)
	}
	if !res.Binary {
		t.Error("NUL-containing file should be flagged binary")
	}

	// Directory refusal.
	if _, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "."}); err == nil {
		t.Error("opening a directory should fail")
	}

	// Containment: an absolute path must be rejected, not read.
	if _, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "../../etc/passwd"}); err == nil {
		t.Error("path traversal must be rejected")
	}
}

func TestEditSave(t *testing.T) {
	root := t.TempDir()
	writeEditFile(t, root, "main.go", "hello world")

	m := newEditManager(testLogger(t))
	opened, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "main.go"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Matching base: plain save.
	res, err := m.save(protocol.CodeCommandPayload{
		Action: "save", Root: root, Path: "main.go",
		Data: []byte("hello again"), BaseMTime: opened.MTime, BaseSize: opened.Size,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if res.Stale || res.Data != nil {
		t.Errorf("clean save should not be stale: %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(got) != "hello again" {
		t.Errorf("disk content = %q", got)
	}

	// Stale save: external edit lands, client saves anyway (last-write-wins)
	// and gets the pre-write content back.
	writeEditFile(t, root, "main.go", "external edit")
	res, err = m.save(protocol.CodeCommandPayload{
		Action: "save", Root: root, Path: "main.go",
		Data: []byte("mine"), BaseMTime: opened.MTime, BaseSize: opened.Size,
	})
	if err != nil {
		t.Fatalf("stale save: %v", err)
	}
	if !res.Stale || string(res.Data) != "external edit" {
		t.Errorf("stale save result = %+v", res)
	}
	got, _ = os.ReadFile(filepath.Join(root, "main.go"))
	if string(got) != "mine" {
		t.Errorf("disk content after stale save = %q", got)
	}

}

func TestEditSaveOversize(t *testing.T) {
	root := t.TempDir()
	writeEditFile(t, root, "big.go", string(make([]byte, protocol.MaxEditorFileBytes+1)))

	m := newEditManager(testLogger(t))
	if _, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "big.go"}); err == nil {
		t.Error("oversized file must be refused")
	}
}

func TestEditWatchPush(t *testing.T) {
	oldPoll := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = oldPoll }()

	root := t.TempDir()
	writeEditFile(t, root, "watched.txt", "v1")

	sender := newFakeEditSender()
	m := newEditManager(testLogger(t))
	m.SetSender(sender)

	if _, err := m.replaceWatch(root, []string{"watched.txt"}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	// Let the poll loop seed the path's stat before editing, so the first
	// push reports a change rather than seeding silently.
	time.Sleep(40 * time.Millisecond)

	// External modify → exactly one content push.
	writeEditFile(t, root, "watched.txt", "v2")
	var p protocol.CodeFileChangedPayload
	var deletedSeen bool
	for i := 0; i < 2; i++ {
		p = sender.waitPush(t)
		if p.Deleted {
			deletedSeen = true
			continue
		}
		break
	}
	if deletedSeen {
		t.Fatalf("first push was a deletion, want content change: %+v", p)
	}
	if p.Path != "watched.txt" || p.Size != 2 {
		t.Errorf("push = %+v", p)
	}

	// A stable file must not be re-pushed: drain for a couple of polls.
	select {
	case extra := <-sender.pushes:
		t.Errorf("unexpected extra push: %+v", extra)
	case <-time.After(60 * time.Millisecond):
	}

	// Self-save: recorded stat prevents a re-push of our own write.
	opened, err := m.open(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "watched.txt"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := m.save(protocol.CodeCommandPayload{
		Action: "save", Root: root, Path: "watched.txt",
		Data: []byte("self"), BaseMTime: opened.MTime, BaseSize: opened.Size,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	select {
	case extra := <-sender.pushes:
		t.Errorf("self-save must not re-push: %+v", extra)
	case <-time.After(60 * time.Millisecond):
	}

	// Delete → one deleted push.
	if err := os.Remove(filepath.Join(root, "watched.txt")); err != nil {
		t.Fatal(err)
	}
	seenDeleted := false
	for !seenDeleted {
		p = sender.waitPush(t)
		if p.Deleted {
			seenDeleted = true
		}
	}
	select {
	case extra := <-sender.pushes:
		t.Errorf("deletion must push exactly once: %+v", extra)
	case <-time.After(60 * time.Millisecond):
	}

	// Unwatch stops everything.
	if _, err := m.removeWatch(root, []string{"watched.txt"}); err != nil {
		t.Fatalf("unwatch: %v", err)
	}
	writeEditFile(t, root, "watched.txt", "recreated")
	select {
	case extra := <-sender.pushes:
		t.Errorf("push after unwatch: %+v", extra)
	case <-time.After(60 * time.Millisecond):
	}
}

func TestEditHandleEnvelopeCorrelation(t *testing.T) {
	root := t.TempDir()
	writeEditFile(t, root, "main.go", "content")

	sender := newFakeEditSender()
	m := newEditManager(testLogger(t))
	m.SetSender(sender)

	body, _ := json.Marshal(protocol.CodeCommandPayload{Action: "open", Root: root, Path: "main.go"})
	m.HandleEnvelope(protocol.Envelope{Type: protocol.MessageCodeCommand, ID: "rpc-1", Payload: body})

	select {
	case rep := <-sender.replies:
		if rep.Type != protocol.MessageCodeResult {
			t.Errorf("reply type = %s, want code.result", rep.Type)
		}
		var res protocol.CodeResultPayload
		if err := json.Unmarshal(rep.Payload, &res); err != nil {
			t.Fatalf("bad reply payload: %v", err)
		}
		if string(res.Data) != "content" {
			t.Errorf("reply data = %q, want %q", res.Data, "content")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply")
	}
}
