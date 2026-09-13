package sessions

import "sync"

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

// Hub routes terminal bytes and open-session results between runtime
// connections and attached browser connections. It is in-memory and scoped
// to a single Control Server process, matching runtimes.Registry.
type Hub struct {
	mu         sync.Mutex
	attached   map[string]BrowserConn
	scrollback map[string][]byte
	pending    map[string]chan error
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{
		attached:   make(map[string]BrowserConn),
		scrollback: make(map[string][]byte),
		pending:    make(map[string]chan error),
	}
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
// any) and appends it to the session's scrollback buffer.
func (h *Hub) Forward(sessionID string, data []byte) {
	h.mu.Lock()
	buf := append(h.scrollback[sessionID], data...)
	if len(buf) > scrollbackLimit {
		buf = buf[len(buf)-scrollbackLimit:]
	}
	h.scrollback[sessionID] = buf
	c := h.attached[sessionID]
	h.mu.Unlock()
	if c != nil {
		_ = c.WriteOutput(data)
	}
}

// Discard drops a session's in-memory state once it is closed/exited and
// will never be attached to again.
func (h *Hub) Discard(sessionID string) {
	h.mu.Lock()
	delete(h.scrollback, sessionID)
	delete(h.attached, sessionID)
	delete(h.pending, sessionID)
	h.mu.Unlock()
}
