package api

import (
	"encoding/json"
	"net/http"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

type createRuntimeRequest struct {
	Name string `json:"name"`
}

type runtimeResponse struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Hostname       string                `json:"hostname"`
	OS             string                `json:"os"`
	Arch           string                `json:"arch"`
	RuntimeVersion string                `json:"runtime_version"`
	Capabilities   protocol.Capabilities `json:"capabilities"`
	Status         string                `json:"status"`
	Online         bool                  `json:"online"`
	EnrolledAt     *time.Time            `json:"enrolled_at"`
	LastSeenAt     *time.Time            `json:"last_seen_at"`
	RevokedAt      *time.Time            `json:"revoked_at"`
	CreatedAt      time.Time             `json:"created_at"`
}

type runtimeWithTokenResponse struct {
	runtimeResponse
	Token string `json:"token"`
}

func toRuntimeResponse(rt runtimes.Runtime, online bool) runtimeResponse {
	return runtimeResponse{
		ID:             rt.ID,
		Name:           rt.Name,
		Hostname:       rt.Hostname,
		OS:             rt.OS,
		Arch:           rt.Arch,
		RuntimeVersion: rt.RuntimeVersion,
		Capabilities:   rt.Capabilities,
		Status:         rt.Status,
		Online:         online,
		EnrolledAt:     rt.EnrolledAt,
		LastSeenAt:     rt.LastSeenAt,
		RevokedAt:      rt.RevokedAt,
		CreatedAt:      rt.CreatedAt,
	}
}

func createRuntimeHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())

		var req createRuntimeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}

		rt, token, err := runtimes.Create(r.Context(), db.Pool, req.Name, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create runtime"})
			return
		}

		audit.Record(r.Context(), db.Pool, audit.EventRuntimeRegistered, audit.Actor{UserID: user.ID, Email: user.Email}, "runtime", rt.ID, map[string]any{"name": rt.Name}, clientIP(r))
		writeJSON(w, http.StatusCreated, runtimeWithTokenResponse{
			runtimeResponse: toRuntimeResponse(rt, false),
			Token:           token,
		})
	}
}

func listRuntimesHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := runtimes.List(r.Context(), db.Pool)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list runtimes"})
			return
		}

		resp := make([]runtimeResponse, 0, len(list))
		for _, rt := range list {
			resp = append(resp, toRuntimeResponse(rt, registry.IsOnline(rt.ID)))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func revokeRuntimeHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeID := r.PathValue("runtimeID")
		user, _ := userFromContext(r.Context())

		ok, err := runtimes.Revoke(r.Context(), db.Pool, runtimeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke runtime"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "runtime not found"})
			return
		}

		audit.Record(r.Context(), db.Pool, audit.EventRuntimeRevoked, audit.Actor{UserID: user.ID, Email: user.Email}, "runtime", runtimeID, nil, clientIP(r))
		registry.Revoke(runtimeID)
		w.WriteHeader(http.StatusNoContent)
	}
}
