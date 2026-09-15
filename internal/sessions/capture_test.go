package sessions

import (
	"bytes"
	"strings"
	"testing"
)

// ADR-045: a command-run session's output tail is captured at the Hub's
// Forward choke point; ordinary terminals capture nothing.
func TestHub_CommandRunCapture(t *testing.T) {
	h := NewHub()
	h.RegisterCapture("s1")
	h.Forward("s1", []byte("out1"))
	h.Forward("s1", []byte("out2"))

	tail := h.TakeCapture("s1")
	if string(tail) != "out1out2" {
		t.Fatalf("tail = %q, want out1out2", tail)
	}
	// Take forgets: one flush per run.
	if again := h.TakeCapture("s1"); again != nil {
		t.Fatalf("second take = %q, want nil", again)
	}
}

func TestHub_CaptureBounded(t *testing.T) {
	h := NewHub()
	h.RegisterCapture("s1")
	h.Forward("s1", bytes.Repeat([]byte("x"), captureLimit+10))

	tail := h.TakeCapture("s1")
	if len(tail) > captureLimit {
		t.Fatalf("tail = %d bytes, want at most %d", len(tail), captureLimit)
	}
	// The kept half is the *last* 64 KiB: it must end in the newest bytes.
	if !strings.HasSuffix(string(tail), strings.Repeat("x", 10)) {
		t.Fatal("tail lost the newest bytes")
	}
}

func TestHub_NoCaptureForOrdinaryTerminals(t *testing.T) {
	h := NewHub()
	h.Forward("s1", []byte("terminal output"))
	if tail := h.TakeCapture("s1"); tail != nil {
		t.Fatalf("ordinary terminal captured %q", tail)
	}
}

func TestHub_CaptureClearedOnDiscard(t *testing.T) {
	h := NewHub()
	h.RegisterCapture("s1")
	h.Forward("s1", []byte("some output"))
	h.Discard("s1")
	if tail := h.TakeCapture("s1"); tail != nil {
		t.Fatalf("post-discard take = %q, want nil", tail)
	}
}
