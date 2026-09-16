package sessions

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeBrowserConn struct {
	mu     sync.Mutex
	writes [][]byte
	closed bool
}

func (c *fakeBrowserConn) WriteOutput(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, append([]byte(nil), data...))
	return nil
}

func (c *fakeBrowserConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *fakeBrowserConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.writes)
}

// waitForWrites polls until c has n writes (Forward's writer goroutine is
// asynchronous) or the 1-second deadline expires.
func waitForWrites(t *testing.T, c *fakeBrowserConn, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for c.writeCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("writeCount on %p never reached %d", c, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHub_ForwardToAttached(t *testing.T) {
	h := NewHub()
	c := &fakeBrowserConn{}
	h.Attach("s1", c)

	h.Forward("s1", []byte("hello"))

	waitForWrites(t, c, 1)
}

func TestHub_ForwardWithNoAttachedViewerDropsButBuffers(t *testing.T) {
	h := NewHub()
	h.Forward("s1", []byte("hello"))

	c := &fakeBrowserConn{}
	h.Attach("s1", c) // reattach should replay buffered scrollback

	if got := c.writeCount(); got != 1 {
		t.Fatalf("writeCount after attach = %d, want 1 (scrollback replay)", got)
	}
}

func TestHub_DetachIgnoresStaleConn(t *testing.T) {
	h := NewHub()
	old := &fakeBrowserConn{}
	newer := &fakeBrowserConn{}

	h.Attach("s1", old)
	h.Attach("s1", newer) // simulates a reattach from a new browser tab

	h.Detach("s1", old) // stale disconnect handler for the old conn

	h.Forward("s1", []byte("x"))
	waitForWrites(t, newer, 1)
}

func TestHub_ForceDetachClosesAndRemovesViewer(t *testing.T) {
	h := NewHub()
	c := &fakeBrowserConn{}
	h.Attach("s1", c)

	h.ForceDetach("s1")

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("expected ForceDetach to close the viewer")
	}

	h.Forward("s1", []byte("x"))
	if got := c.writeCount(); got != 0 {
		t.Fatalf("writeCount after ForceDetach = %d, want 0", got)
	}
}

func TestHub_AwaitOpenResolvedBySuccess(t *testing.T) {
	h := NewHub()
	ch := h.AwaitOpen("s1")

	h.ResolveOpen("s1", nil)

	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitOpen channel never resolved")
	}
}

func TestHub_AwaitOpenResolvedByError(t *testing.T) {
	h := NewHub()
	ch := h.AwaitOpen("s1")

	wantErr := errors.New("boom")
	h.ResolveOpen("s1", wantErr)

	select {
	case err := <-ch:
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitOpen channel never resolved")
	}
}

func TestHub_IdleSessionsFlaggedOnceResetOnActivityClearedOnDiscard(t *testing.T) {
	h := NewHub()
	const threshold = 20 * time.Millisecond

	h.Forward("s1", []byte("x"))
	if got := h.IdleSessions(threshold); len(got) != 0 {
		t.Fatalf("IdleSessions immediately after Forward = %v, want none", got)
	}

	time.Sleep(2 * threshold)
	got := h.IdleSessions(threshold)
	if len(got) != 1 || got[0] != "s1" {
		t.Fatalf("IdleSessions after threshold = %v, want [s1]", got)
	}

	// Already flagged: a repeat scan must not notify again.
	if got := h.IdleSessions(threshold); len(got) != 0 {
		t.Fatalf("IdleSessions on repeat scan = %v, want none (already flagged)", got)
	}

	// New activity resets the flag.
	h.Forward("s1", []byte("y"))
	time.Sleep(2 * threshold)
	if got := h.IdleSessions(threshold); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("IdleSessions after new activity + threshold = %v, want [s1]", got)
	}

	h.Discard("s1")
	if got := h.IdleSessions(threshold); len(got) != 0 {
		t.Fatalf("IdleSessions after Discard = %v, want none", got)
	}
}

func TestHub_DiscardClearsScrollbackAndAttachment(t *testing.T) {
	h := NewHub()
	c := &fakeBrowserConn{}
	h.Forward("s1", []byte("buffered"))
	h.Attach("s1", c) // consumes the buffered replay

	h.Discard("s1")

	fresh := &fakeBrowserConn{}
	h.Attach("s1", fresh)
	if got := fresh.writeCount(); got != 0 {
		t.Fatalf("writeCount after Discard+reattach = %d, want 0 (scrollback cleared)", got)
	}
}

// blockingBrowserConn never finishes a write: a viewer that stopped reading
// while keeping its socket open. (The real wsConn's write carries a 5s
// timeout; the blocking fake exercises the overflow path without it.)
type blockingBrowserConn struct{}

func (c *blockingBrowserConn) WriteOutput(data []byte) error {
	select {}
}

func (c *blockingBrowserConn) Close() {}

// TestHub_QueueFullDetachesWedgedViewer covers ADR-050's head-of-line fix:
// a viewer that stops reading must be detached by the overflow path instead
// of stalling Forward, and its scrollback must survive the detach.
func TestHub_QueueFullDetachesWedgedViewer(t *testing.T) {
	h := NewHub()
	wedged := &blockingBrowserConn{}
	h.Attach("s1", wedged)

	// One frame is parked inside the wedged writer, so overflow needs more
	// than depth+1 forwards regardless of goroutine scheduling.
	for i := 0; i < outboundQueueDepth+5; i++ {
		h.Forward("s1", []byte("x"))
	}

	deadline := time.Now().Add(time.Second)
	for h.attachedConn("s1") != nil {
		if time.Now().After(deadline) {
			t.Fatal("wedged viewer still attached after queue overflow")
		}
		time.Sleep(time.Millisecond)
	}

	// Scrollback survives; a fresh viewer replays it.
	fresh := &fakeBrowserConn{}
	h.Attach("s1", fresh)
	if got := fresh.writeCount(); got != 1 {
		t.Fatalf("reattach after wedged viewer detached: writeCount = %d, want 1", got)
	}
}

func TestHub_CaptureTailCapped(t *testing.T) {
	h := NewHub()
	h.RegisterCapture("s1")

	h.Forward("s1", bytes.Repeat([]byte("x"), captureLimit/2))
	h.Forward("s1", bytes.Repeat([]byte("y"), captureLimit)) // pushes past the cap

	tail := h.TakeCapture("s1")
	if len(tail) > captureLimit {
		t.Fatalf("capture len = %d, want <= %d", len(tail), captureLimit)
	}
	if !bytes.Equal(tail[len(tail)-len("yyyy"):], []byte("yyyy")) {
		t.Fatal("capture tail should end with the newest bytes")
	}
}

func TestHub_ScrollbackTrimmed(t *testing.T) {
	h := NewHub()
	h.Forward("s1", bytes.Repeat([]byte("a"), scrollbackLimit))
	h.Forward("s1", []byte("END"))

	c := &fakeBrowserConn{}
	h.Attach("s1", c) // replay is the trimmed tail, exactly scrollbackLimit bytes
	waitForWrites(t, c, 1)

	c.mu.Lock()
	replayed := c.writes[0]
	c.mu.Unlock()
	if len(replayed) != scrollbackLimit {
		t.Fatalf("replayed len = %d, want %d", len(replayed), scrollbackLimit)
	}
	if !bytes.HasSuffix(replayed, []byte("END")) {
		t.Fatal("replayed tail should end with the newest bytes")
	}
}
