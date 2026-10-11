package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"devstationone/internal/push"
)

// maxPushBody bounds subscription request bodies; a real PushSubscription
// JSON is well under 1 KiB.
const maxPushBody = 8 << 10

// pushUnavailable answers when push.New failed at startup (logged then).
func pushUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "web push is unavailable"})
}

// pushPublicKeyHandler GET /api/v1/push/public-key — the VAPID key a browser
// needs for pushManager.subscribe.
func pushPublicKeyHandler(svc *push.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			pushUnavailable(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"public_key": svc.PublicKey()})
	}
}

// pushSubscribeHandler POST /api/v1/push/subscriptions — registers the
// browser's PushSubscription for the signed-in user. The user always comes
// from the session, never the body.
func pushSubscribeHandler(svc *push.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			pushUnavailable(w)
			return
		}
		user, _ := userFromContext(r.Context())
		var req struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Keys.P256dh == "" || req.Keys.Auth == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "keys.p256dh and keys.auth are required"})
			return
		}
		if err := push.ValidEndpoint(req.Endpoint); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ua := r.UserAgent()
		if len(ua) > 256 {
			ua = ua[:256]
		}
		if err := svc.Subscribe(r.Context(), user.ID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth, strings.TrimSpace(ua)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save subscription"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// pushUnsubscribeHandler DELETE /api/v1/push/subscriptions — removes the
// caller's own subscription for the given endpoint.
func pushUnsubscribeHandler(svc *push.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			pushUnavailable(w)
			return
		}
		user, _ := userFromContext(r.Context())
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&req); err != nil || req.Endpoint == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint is required"})
			return
		}
		if err := svc.Unsubscribe(r.Context(), user.ID, req.Endpoint); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove subscription"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
