package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"devstationone/internal/auth"
	"devstationone/internal/notifications"
)

// killSession sends SIGKILL to s and waits for it to reach "exited", which
// is the real MessageSessionExit path that records a task.completed
// notification for the project owner (internal/api/runtime_ws.go).
func killSession(t *testing.T, router http.Handler, cookie *http.Cookie, s termSessionResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"signal": "SIGKILL"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+s.ID+"/signal", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("signal: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	pollUntil(t, 5*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.ID, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var got termSessionResponse
		json.NewDecoder(rec.Body).Decode(&got)
		return got.Status == "exited"
	})
}

func TestNotificationsHTTP_ListScopedToOwnerAndUnreadCount(t *testing.T) {
	router, server, adminCookie, projectID, db := sessionsTestSetup(t)
	_ = server

	if _, err := auth.CreateUser(t.Context(), db.Pool, "bob@example.com", "password123", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCookie := loginAs(t, router, "bob@example.com", "password123")

	s := createSession(t, router, adminCookie, projectID, "bash")
	conn := dialSessionStream(t, server, adminCookie, s.ID)
	defer conn.Close(websocket.StatusNormalClosure, "")
	writeInput(t, conn, "sleep 30\n")
	time.Sleep(300 * time.Millisecond)
	killSession(t, router, adminCookie, s)

	// The owner sees the task.completed notification, unread.
	var list struct {
		Notifications []notifications.Notification `json:"notifications"`
		Total         int                          `json:"total"`
	}
	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		list = struct {
			Notifications []notifications.Notification `json:"notifications"`
			Total         int                          `json:"total"`
		}{}
		json.NewDecoder(rec.Body).Decode(&list)
		return list.Total == 1
	})
	if list.Notifications[0].Type != notifications.TypeTaskCompleted {
		t.Fatalf("type = %q, want %q", list.Notifications[0].Type, notifications.TypeTaskCompleted)
	}
	if list.Notifications[0].ReadAt != nil {
		t.Fatalf("ReadAt = %v, want nil (unread)", list.Notifications[0].ReadAt)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var unread struct {
		Unread int `json:"unread"`
	}
	json.NewDecoder(rec.Body).Decode(&unread)
	if unread.Unread != 1 {
		t.Fatalf("unread = %d, want 1", unread.Unread)
	}

	// Bob (not the project owner) sees none of it.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var bobList struct {
		Total int `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&bobList)
	if bobList.Total != 0 {
		t.Fatalf("bob's total = %d, want 0", bobList.Total)
	}

	// Bob cannot mark admin's notification read.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+list.Notifications[0].ID+"/read", nil)
	req.AddCookie(bobCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob mark-read: status = %d, want 404", rec.Code)
	}

	// The owner can mark it read, clearing the unread count.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+list.Notifications[0].ID+"/read", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mark-read: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	unread = struct {
		Unread int `json:"unread"`
	}{}
	json.NewDecoder(rec.Body).Decode(&unread)
	if unread.Unread != 0 {
		t.Fatalf("unread after mark-read = %d, want 0", unread.Unread)
	}
}

func TestNotificationsHTTP_MarkAllRead(t *testing.T) {
	router, server, adminCookie, projectID, _ := sessionsTestSetup(t)

	for i := 0; i < 2; i++ {
		s := createSession(t, router, adminCookie, projectID, "bash")
		conn := dialSessionStream(t, server, adminCookie, s.ID)
		writeInput(t, conn, "sleep 30\n")
		time.Sleep(300 * time.Millisecond)
		killSession(t, router, adminCookie, s)
		conn.Close(websocket.StatusNormalClosure, "")
	}

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var unread struct {
			Unread int `json:"unread"`
		}
		json.NewDecoder(rec.Body).Decode(&unread)
		return unread.Unread == 2
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil)
	req.AddCookie(adminCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("read-all: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var unread struct {
		Unread int `json:"unread"`
	}
	json.NewDecoder(rec.Body).Decode(&unread)
	if unread.Unread != 0 {
		t.Fatalf("unread after read-all = %d, want 0", unread.Unread)
	}
}
