package runtimes

import "sync"

// Conn is the subset of a runtime's live connection the registry needs in
// order to notify it of revocation and close it.
type Conn interface {
	Revoke() error
}

// Registry tracks currently-connected runtimes in memory. It is scoped to a
// single Control Server process; a multi-instance deployment would need a
// shared broker, which is out of scope for this MVP (see DECISIONS.md).
type Registry struct {
	mu    sync.Mutex
	conns map[string]Conn
}

// NewRegistry creates an empty connection registry.
func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]Conn)}
}

// Register records that runtimeID's live connection is c.
func (r *Registry) Register(runtimeID string, c Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[runtimeID] = c
}

// Unregister removes runtimeID's connection, but only if it is still c —
// this avoids a stale disconnect handler evicting a newer reconnection.
func (r *Registry) Unregister(runtimeID string, c Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[runtimeID] == c {
		delete(r.conns, runtimeID)
	}
}

// IsOnline reports whether runtimeID currently has a live connection.
func (r *Registry) IsOnline(runtimeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.conns[runtimeID]
	return ok
}

// Revoke notifies and closes runtimeID's live connection, if any, reporting
// whether one was found.
func (r *Registry) Revoke(runtimeID string) bool {
	r.mu.Lock()
	c, ok := r.conns[runtimeID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	_ = c.Revoke()
	return true
}
