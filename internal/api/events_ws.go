package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/events"
)

// eventsPingInterval keeps the browser's events connection alive across
// intermediate proxies during quiet stretches between real events.
const eventsPingInterval = 30 * time.Second

// eventsStreamHandler serves the live dashboard/notification push channel
// (WSS /api/v1/events/stream). Each signed-in browser tab gets its own
// subscription to the shared events.Bus, scoped to its own user's events
// plus every broadcast event (see events.Event.UserID).
func eventsStreamHandler(bus *events.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())

		conn, err := websocket.Accept(w, r, wsAcceptOptions)
		if err != nil {
			return // Accept already wrote the HTTP error response.
		}
		defer conn.CloseNow()

		ch, cancel := bus.Subscribe(user.ID)
		defer cancel()

		ctx := r.Context()

		// The browser never sends anything on this connection; reading in
		// the background is only how a closed/dead connection is detected.
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()

		ticker := time.NewTicker(eventsPingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-closed:
				return
			case ev := <-ch:
				if !writeEvent(ctx, conn, ev) {
					return
				}
			case <-ticker.C:
				if !writeEvent(ctx, conn, events.Event{Type: "ping"}) {
					return
				}
			}
		}
	}
}

func writeEvent(ctx context.Context, conn *websocket.Conn, ev events.Event) bool {
	data, err := json.Marshal(ev)
	if err != nil {
		return true // skip a malformed event, don't kill the connection over it
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data) == nil
}

// publishRuntimeStatus broadcasts a runtime's online/offline transition to
// every connected browser — runtimes are shared infrastructure, visible to
// any signed-in user (see browseRuntimeHandler).
func publishRuntimeStatus(bus *events.Bus, runtimeID string, online bool) {
	bus.Publish(events.Event{
		Type:    "runtime.status",
		Payload: map[string]any{"runtime_id": runtimeID, "online": online},
	})
}

// publishProjectStatus notifies projectID's owner that its session activity
// changed, so an open dashboard can refetch (docs/IMPLEMENTATION_PLAN.md
// Slice 14). Projects are owned per-user, unlike runtimes, so this is
// scoped to ownerID rather than broadcast.
func publishProjectStatus(bus *events.Bus, ownerID, projectID string) {
	bus.Publish(events.Event{
		Type:    "project.status",
		UserID:  &ownerID,
		Payload: map[string]string{"project_id": projectID},
	})
}
