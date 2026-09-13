package sessions

import (
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

func TestHub_ForwardToAttached(t *testing.T) {
	h := NewHub()
	c := &fakeBrowserConn{}
	h.Attach("s1", c)

	h.Forward("s1", []byte("hello"))

	if got := c.writeCount(); got != 1 {
		t.Fatalf("writeCount = %d, want 1", got)
	}
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
	if got := newer.writeCount(); got != 1 {
		t.Fatalf("newer.writeCount = %d, want 1 (stale Detach must not evict it)", got)
	}
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
