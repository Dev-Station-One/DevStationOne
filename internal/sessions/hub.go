package sessions

import (
	"sync"
	"time"
)

// scrollbackLimit bounds how many recent output bytes are buffered per
// session so a reattaching browser sees recent output immediately, without
// unbounded memory growth. This is an in-memory convenience, not a
// persistence guarantee: bytes produced while the Control Server is
// restarted, or while the runtime connection itself is down, are lost.
// ponytail: fixed per-process cap; a shared store would be needed if the
// Control Server were ever horizontally scaled (see ADR-012).
const scrollbackLimit = 256 * 1024

// BrowserConn is the subset of an attached browser WebSocket connection the
// Hub needs in order to forward terminal output to it, and to force it
// closed (e.g. when another device explicitly takes over the session).
type BrowserConn interface {
	WriteOutput(data []byte) error
	Close()
}

// captureLimit is how much of one session's output is captured for the
// Quick Command run history (ADR-045's bounded persistence exception).
const captureLimit = 64 * 1024

// Hub routes terminal bytes and open-session results between runtime
// connections and attached browser connections. It is in-memory and scoped
// to a single Control Server process, matching runtimes.Registry.
type Hub struct {
	mu           sync.Mutex
	attached     map[string]BrowserConn
	scrollback   map[string][]byte
	pending      map[string]chan error
	lastActivity map[string]time.Time
	idleNotified map[string]bool
	// captures holds a bounded output tail per command-run session
	// (ADR-045): Forward already sees every output byte, so the capture
	// rides this existing choke point instead of a new protocol message.
	// Ordinary terminals register nothing and capture zero bytes.
	captures map[string][]byte
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{
		attached:     make(map[string]BrowserConn),
		scrollback:   make(map[string][]byte),
		pending:      make(map[string]chan error),
		lastActivity: make(map[string]time.Time),
		idleNotified: make(map[string]bool),
		captures:     make(map[string][]byte),
	}
}

// AttachedCount returns how many sessions currently have a live browser
// connection attached (docs/SECURITY.md "metrics").
func (h *Hub) AttachedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.attached)
}

// AwaitOpen registers a wait for the runtime's response to a just-sent
// session.open command, returning a channel that receives nil on
// session.ready or an error on session.error.
func (h *Hub) AwaitOpen(sessionID string) <-chan error {
	ch := make(chan error, 1)
	h.mu.Lock()
	h.pending[sessionID] = ch
	h.mu.Unlock()
	return ch
}

// ResolveOpen delivers the runtime's session.ready/session.error response
// to a pending AwaitOpen call, if one is still waiting.
func (h *Hub) ResolveOpen(sessionID string, err error) {
	h.mu.Lock()
	ch := h.pending[sessionID]
	delete(h.pending, sessionID)
	h.mu.Unlock()
	if ch != nil {
		ch <- err
	}
}

// Attach records c as sessionID's current viewer and immediately replays
// any buffered scrollback to it.
func (h *Hub) Attach(sessionID string, c BrowserConn) {
	h.mu.Lock()
	h.attached[sessionID] = c
	buf := h.scrollback[sessionID]
	h.mu.Unlock()
	if len(buf) > 0 {
		_ = c.WriteOutput(buf)
	}
}

// Detach removes c as sessionID's viewer, but only if it is still the
// current one — a newer attach may have already replaced it.
func (h *Hub) Detach(sessionID string, c BrowserConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attached[sessionID] == c {
		delete(h.attached, sessionID)
	}
}

// ForceDetach closes and removes whichever viewer is currently attached to
// sessionID, without discarding its scrollback — the PTY is still alive
// and a future attach should still see recent output. Used by the explicit
// detach action, as opposed to Discard's full teardown on close/exit.
func (h *Hub) ForceDetach(sessionID string) {
	h.mu.Lock()
	c := h.attached[sessionID]
	delete(h.attached, sessionID)
	h.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// Forward delivers runtime output for sessionID to its attached browser (if
// any) and appends it to the session's scrollback buffer. It is also the
// single choke point every runtime output byte passes through, so it doubles
// as the "this session just produced output" signal for the awaiting-input
// idle heuristic (docs/IMPLEMENTATION_PLAN.md Slice 15): it resets the idle
// clock and clears any prior idle notification flag.
func (h *Hub) Forward(sessionID string, data []byte) {
	h.mu.Lock()
	buf := append(h.scrollback[sessionID], data...)
	if len(buf) > scrollbackLimit {
		buf = buf[len(buf)-scrollbackLimit:]
	}
	h.scrollback[sessionID] = buf
	if _, ok := h.captures[sessionID]; ok {
		cap := append(h.captures[sessionID], data...)
		if len(cap) > captureLimit {
			cap = cap[len(cap)-captureLimit:]
		}
		h.captures[sessionID] = cap
	}
	h.lastActivity[sessionID] = time.Now()
	delete(h.idleNotified, sessionID)
	c := h.attached[sessionID]
	h.mu.Unlock()
	if c != nil {
		_ = c.WriteOutput(data)
	}
}

// RegisterCapture starts buffering sessionID's output tail for the Quick
// Command run history (ADR-045). Must be called before the session can
// produce output, i.e. before session.open is dispatched.
func (h *Hub) RegisterCapture(sessionID string) {
	h.mu.Lock()
	h.captures[sessionID] = nil
	h.mu.Unlock()
}

// TakeCapture returns and forgets the buffered output tail for sessionID —
// nil when the session has no capture registered (an ordinary terminal, or
// one whose run row was already finalized). Called once at session exit.
func (h *Hub) TakeCapture(sessionID string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	tail := h.captures[sessionID]
	delete(h.captures, sessionID)
	return tail
}

// IdleSessions returns the IDs of sessions that have produced output before
// (via Forward) but not within threshold, and have not already been flagged
// idle since their last activity. Each returned session is marked flagged so
// a repeat scan doesn't notify again until Forward next resets the flag.
func (h *Hub) IdleSessions(threshold time.Duration) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-threshold)
	var out []string
	for id, t := range h.lastActivity {
		if h.idleNotified[id] || t.After(cutoff) {
			continue
		}
		h.idleNotified[id] = true
		out = append(out, id)
	}
	return out
}

// Discard drops a session's in-memory state once it is closed/exited and
// will never be attached to again.
func (h *Hub) Discard(sessionID string) {
	h.mu.Lock()
	delete(h.scrollback, sessionID)
	delete(h.attached, sessionID)
	delete(h.pending, sessionID)
	delete(h.lastActivity, sessionID)
	delete(h.idleNotified, sessionID)
	delete(h.captures, sessionID)
	h.mu.Unlock()
}
