package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devstationone/internal/auth"
)

func pushRequest(method, path string, body any, cookie *http.Cookie) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestPushHTTP_SubscribeLifecycleAndValidation(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
	if _, err := auth.CreateUser(t.Context(), db.Pool, "pusher@example.com", "password123", false); err != nil {
		t.Fatal(err)
	}
	cookie := loginAs(t, router, "pusher@example.com", "password123")

	do := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Unauthenticated callers get nothing, not even the public key.
	for _, req := range []*http.Request{
		pushRequest(http.MethodGet, "/api/v1/push/public-key", nil, nil),
		pushRequest(http.MethodPost, "/api/v1/push/subscriptions", map[string]any{}, nil),
		pushRequest(http.MethodDelete, "/api/v1/push/subscriptions", map[string]any{}, nil),
	} {
		if rec := do(req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated: %d, want 401", req.Method, req.URL.Path, rec.Code)
		}
	}

	rec := do(pushRequest(http.MethodGet, "/api/v1/push/public-key", nil, cookie))
	var key struct {
		PublicKey string `json:"public_key"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &key) != nil || len(key.PublicKey) < 80 {
		t.Fatalf("public-key: %d %s", rec.Code, rec.Body.String())
	}

	good := map[string]any{
		"endpoint": "https://fcm.googleapis.com/fcm/send/abc",
		"keys":     map[string]string{"p256dh": "BPk", "auth": "au"},
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"http endpoint", map[string]any{"endpoint": "http://fcm.googleapis.com/x", "keys": good["keys"]}, http.StatusBadRequest},
		{"loopback endpoint", map[string]any{"endpoint": "https://127.0.0.1/x", "keys": good["keys"]}, http.StatusBadRequest},
		{"missing keys", map[string]any{"endpoint": good["endpoint"]}, http.StatusBadRequest},
		{"valid", good, http.StatusNoContent},
		{"valid again (idempotent)", good, http.StatusNoContent},
	}
	for _, c := range cases {
		if rec := do(pushRequest(http.MethodPost, "/api/v1/push/subscriptions", c.body, cookie)); rec.Code != c.want {
			t.Fatalf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}

	count := func() int {
		var n int
		db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM push_subscriptions`).Scan(&n)
		return n
	}
	if count() != 1 {
		t.Fatalf("subscriptions = %d, want 1", count())
	}

	if rec := do(pushRequest(http.MethodDelete, "/api/v1/push/subscriptions", map[string]string{}, cookie)); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete without endpoint: %d, want 400", rec.Code)
	}
	if rec := do(pushRequest(http.MethodDelete, "/api/v1/push/subscriptions", map[string]string{"endpoint": good["endpoint"].(string)}, cookie)); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d, want 204", rec.Code)
	}
	if count() != 0 {
		t.Fatalf("subscriptions after delete = %d, want 0", count())
	}
}
