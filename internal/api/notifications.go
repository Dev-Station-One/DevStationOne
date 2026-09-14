package api

import (
	"net/http"
	"strconv"

	"devstationone/internal/database"
	"devstationone/internal/notifications"
)

// listNotificationsHandler returns the current user's own notifications,
// most recent first. There is no cross-user access: userID always comes
// from the session, never a request parameter.
func listNotificationsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		list, total, err := notifications.ListForUser(r.Context(), db.Pool, user.ID, limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list notifications"})
			return
		}
		if list == nil {
			list = []notifications.Notification{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"notifications": list, "total": total})
	}
}

func unreadNotificationCountHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		n, err := notifications.UnreadCount(r.Context(), db.Pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count notifications"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"unread": n})
	}
}

func markNotificationReadHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		ok, err := notifications.MarkRead(r.Context(), db.Pool, user.ID, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mark notification read"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func markAllNotificationsReadHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r.Context())
		if _, err := notifications.MarkAllRead(r.Context(), db.Pool, user.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mark notifications read"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
