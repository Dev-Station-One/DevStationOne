package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
)

type auditEventResponse struct {
	ID           string         `json:"id"`
	OccurredAt   time.Time      `json:"occurred_at"`
	EventType    string         `json:"event_type"`
	ActorUserID  *string        `json:"actor_user_id"`
	ActorEmail   string         `json:"actor_email"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Detail       map[string]any `json:"detail"`
	IPAddress    string         `json:"ip_address"`
}

func toAuditEventResponse(e audit.Event) auditEventResponse {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	return auditEventResponse{
		ID:           e.ID,
		OccurredAt:   e.OccurredAt,
		EventType:    e.EventType,
		ActorUserID:  e.ActorUserID,
		ActorEmail:   e.ActorEmail,
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
		Detail:       detail,
		IPAddress:    e.IPAddress,
	}
}

// parseAuditTime parses an RFC3339 query parameter, writing a 400 and
// returning ok=false if name is present but malformed.
func parseAuditTime(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": name + " must be RFC3339"})
		return nil, false
	}
	return &t, true
}

func listAuditEventsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		since, ok := parseAuditTime(w, r, "since")
		if !ok {
			return
		}
		until, ok := parseAuditTime(w, r, "until")
		if !ok {
			return
		}

		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))

		list, total, err := audit.List(r.Context(), db.Pool, audit.Filter{
			EventType:    q.Get("event_type"),
			ActorUserID:  q.Get("actor_user_id"),
			ResourceType: q.Get("resource_type"),
			Since:        since,
			Until:        until,
			Limit:        limit,
			Offset:       offset,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list audit events"})
			return
		}

		resp := make([]auditEventResponse, 0, len(list))
		for _, e := range list {
			resp = append(resp, toAuditEventResponse(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": resp, "total": total})
	}
}

type auditSettingsResponse struct {
	RetentionDays int       `json:"retention_days"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toAuditSettingsResponse(s audit.Settings) auditSettingsResponse {
	return auditSettingsResponse{RetentionDays: s.RetentionDays, UpdatedAt: s.UpdatedAt}
}

func getAuditSettingsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := audit.GetSettings(r.Context(), db.Pool)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load audit settings"})
			return
		}
		writeJSON(w, http.StatusOK, toAuditSettingsResponse(s))
	}
}

func updateAuditSettingsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RetentionDays *int `json:"retention_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.RetentionDays == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retention_days is required"})
			return
		}
		s, err := audit.UpdateSettings(r.Context(), db.Pool, *req.RetentionDays)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update audit settings"})
			return
		}
		writeJSON(w, http.StatusOK, toAuditSettingsResponse(s))
	}
}
