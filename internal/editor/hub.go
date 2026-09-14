// Package editor supports the Code tab's live editing: an in-process hub
// that fans whole-file change pushes out to the project's subscribed editor
// streams, and Postgres persistence for a user's open-editor-tab state. Like
// runtimes.Registry and events.Bus it is scoped to a single Control Server
// process (ADR-012).
package editor

import (
	"sync"
	"time"
)

// subscriberBuffer bounds how many pending messages a slow browser tab can
// fall behind by before new messages are dropped for it rather than blocking
// the publisher.
// ponytail: fixed buffer, no backpressure signal; editor pushes are
// low-frequency (1 poll tick, per changed file).
const subscriberBuffer = 32

// Message is one message delivered to an editor stream connection. Data is
// the whole file (binary/oversized files are refused before reaching here);
// MTime/Size are the on-disk fingerprint the client should use as its next
// save's base.
type Message struct {
	Type    string    `json:"type"` // content | deleted | error
	Path    string    `json:"path"`
	MTime   time.Time `json:"mtime,omitempty"`
	Size    int64     `json:"size,omitempty"`
	Data    string    `json:"data,omitempty"`
	Error   string    `json:"error,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
}

// sub is one connected editor stream: its delivery channel and the open
// file path set it declared with its latest "watch" message.
type sub struct {
	ch    chan Message
	paths map[string]struct{}
}

// Hub fans published Messages out to a project's subscribed editor
// connections and tracks the per-project union of their open-file sets, so
// the WebSocket handler can forward a single idempotent watch set to the
// runtime.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*sub]struct{}
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[*sub]struct{})}
}

// Subscribe registers a new subscriber for projectID's pushes, returning
// the subscription handle, the channel to receive on, and a cancel func
// that unsubscribes it. The caller must call cancel when done.
func (h *Hub) Subscribe(projectID string) (*sub, <-chan Message, func()) {
	s := &sub{ch: make(chan Message, subscriberBuffer), paths: make(map[string]struct{})}
	h.mu.Lock()
	if h.subs[projectID] == nil {
		h.subs[projectID] = make(map[*sub]struct{})
	}
	h.subs[projectID][s] = struct{}{}
	h.mu.Unlock()
	return s, s.ch, func() { h.Drop(projectID, s) }
}

// SetPaths replaces one subscriber's open-file path set and returns the
// per-project union of every subscriber's paths — the watch set to send to
// the runtime.
func (h *Hub) SetPaths(projectID string, s *sub, paths []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, alive := h.subs[projectID][s]; !alive {
		return nil
	}
	s.paths = make(map[string]struct{}, len(paths))
	for _, p := range paths {
		s.paths[p] = struct{}{}
	}
	return h.unionLocked(projectID)
}

// unionLocked recomputes projectID's union of every live subscriber's paths.
func (h *Hub) unionLocked(projectID string) []string {
	union := make(map[string]struct{})
	for s := range h.subs[projectID] {
		for p := range s.paths {
			union[p] = struct{}{}
		}
	}
	if len(union) == 0 {
		return nil
	}
	out := make([]string, 0, len(union))
	for p := range union {
		out = append(out, p)
	}
	return out
}

// Drop unsubscribes s and returns the remaining union, nil when the project
// has no subscribers or open paths left.
func (h *Hub) Drop(projectID string, s *sub) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs[projectID], s)
	s.paths = nil
	if len(h.subs[projectID]) == 0 {
		delete(h.subs, projectID)
		return nil
	}
	return h.unionLocked(projectID)
}

// Publish delivers m to every subscriber of projectID. A subscriber whose
// buffer is full is skipped rather than blocking the publisher.
func (h *Hub) Publish(projectID string, m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[projectID] {
		select {
		case s.ch <- m:
		default:
		}
	}
}
