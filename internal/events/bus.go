// Package events implements a small in-process publish/subscribe bus used to
// push live updates (runtime online/offline, project session-status changes)
// to connected browser clients over the Control Server's events WebSocket
// (see internal/api's events_ws.go). It is scoped to a single Control Server
// process, the same limitation already documented on runtimes.Registry and
// sessions.Hub — a multi-instance deployment would need a shared broker
// (ADR-012), out of scope for this MVP.
package events

import "sync"

// subscriberBuffer bounds how many pending events a slow browser tab can
// fall behind by before new events are dropped for it rather than blocking
// the publisher.
// ponytail: fixed buffer, no backpressure signal; fine for a low-frequency
// status channel, revisit if a future event type is high-volume.
const subscriberBuffer = 16

// Event is one message delivered to subscribed browsers.
type Event struct {
	Type string `json:"type"`
	// UserID restricts delivery to one user's own subscriptions; nil
	// broadcasts to every connected browser (used for runtime status —
	// runtimes are shared infrastructure, not owned per-user).
	UserID  *string `json:"-"`
	Payload any     `json:"payload,omitempty"`
}

// Bus fans out published Events to subscribed browser connections.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]string
	taps []func(Event)
}

// Tap registers fn to be called with every published Event, regardless of
// user scope. fn runs on the publisher's goroutine, so it must return
// quickly (hand slow work to its own goroutine). Taps cannot be removed;
// they live as long as the Bus.
func (b *Bus) Tap(fn func(Event)) {
	b.mu.Lock()
	b.taps = append(b.taps, fn)
	b.mu.Unlock()
}

// NewBus creates an empty Bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[chan Event]string)}
}

// Subscribe registers a new subscriber for userID's own events plus every
// broadcast event, returning the channel to receive on and a cancel func
// that unregisters it. The caller must call cancel when done.
func (b *Bus) Subscribe(userID string) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = userID
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Publish delivers e to every matching subscriber. A subscriber whose
// buffer is full is skipped rather than blocking the publisher.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	for ch, userID := range b.subs {
		if e.UserID != nil && *e.UserID != userID {
			continue
		}
		select {
		case ch <- e:
		default:
		}
	}
	taps := b.taps
	b.mu.Unlock()
	for _, fn := range taps {
		fn(e)
	}
}
