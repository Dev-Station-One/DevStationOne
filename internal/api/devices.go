package api

// Native-client device pairing and management (docs/DECISIONS.md ADR-053).
// Pairing exchanges a CLI-minted one-time code for a device row bound to
// the code's user; management lists and revokes devices. Authentication
// itself is the deviceauth Sig scheme handled by requirement of identity
// in middleware.go.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/auth"
	"devstationone/internal/database"
	"devstationone/internal/devices"
)

type pairDeviceRequest struct {
	Code      string `json:"code"`
	PublicKey string `json:"public_key"`
}

type deviceResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func toDeviceResponse(d devices.Device) deviceResponse {
	return deviceResponse{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastUsedAt: d.LastUsedAt, RevokedAt: d.RevokedAt}
}

// pairDeviceHandler completes a pairing: the phone posts the one-time code
// and its ECDSA P-256 public key (base64 SPKI); the server burns the code,
// registers the device under the code's user, and returns the device ID the
// client will carry in every future Sig header.
func pairDeviceHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req pairDeviceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.PublicKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code and public_key are required"})
			return
		}
		if err := devices.ValidatePublicKey(req.PublicKey); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		userID, deviceName, err := devices.ConsumePairingCode(r.Context(), db.Pool, req.Code)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid, expired, or already-used pairing code"})
			return
		}

		d, err := devices.CreateDevice(r.Context(), db.Pool, userID, deviceName, req.PublicKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to register device"})
			return
		}

		owner, _ := auth.GetUserByID(r.Context(), db.Pool, userID)
		audit.Record(r.Context(), db.Pool, audit.EventDevicePaired, audit.Actor{UserID: owner.ID, Email: owner.Email}, "device", d.ID, map[string]any{"name": d.Name}, clientIP(r))
		writeJSON(w, http.StatusCreated, deviceResponse{ID: d.ID, Name: d.Name})
	}
}

// listDevicesHandler returns the session/device-authenticated user's paired
// devices (Settings page).
func listDevicesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		list, err := devices.ListForUser(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list devices"})
			return
		}
		out := make([]deviceResponse, 0, len(list))
		for _, d := range list {
			out = append(out, toDeviceResponse(d))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// revokeDeviceHandler revokes a device. Users revoke their own devices;
// administrators revoke anyone's (the per-user view on the Users page).
func revokeDeviceHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		d, err := devices.Get(r.Context(), db.Pool, r.PathValue("deviceID"))
		if errors.Is(err, devices.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such device"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "device lookup failed"})
			return
		}
		if d.UserID != user.ID && !user.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "not your device"})
			return
		}
		ok, err := devices.Revoke(r.Context(), db.Pool, d.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "revoke failed"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such device"})
			return
		}
		audit.Record(r.Context(), db.Pool, audit.EventDeviceRevoked, audit.Actor{UserID: user.ID, Email: user.Email}, "device", d.ID, map[string]any{"owner": d.UserID, "name": d.Name}, clientIP(r))
		writeJSON(w, http.StatusNoContent, nil)
	}
}

// listUserDevicesHandler (admin) lists one user's devices for the Users
// manager page.
func listUserDevicesHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := devices.ListForUser(r.Context(), db.Pool, r.PathValue("userID"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list devices"})
			return
		}
		out := make([]deviceResponse, 0, len(list))
		for _, d := range list {
			out = append(out, toDeviceResponse(d))
		}
		writeJSON(w, http.StatusOK, out)
	}
}
