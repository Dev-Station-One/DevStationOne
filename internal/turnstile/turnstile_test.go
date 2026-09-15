package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withFakeSiteVerify(t *testing.T, respond func(w http.ResponseWriter)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("secret") == "" || r.FormValue("response") == "" {
			t.Fatalf("missing secret or response in request: %v", r.Form)
		}
		respond(w)
	}))
	t.Cleanup(server.Close)
	orig := SiteVerifyURL
	SiteVerifyURL = server.URL
	t.Cleanup(func() { SiteVerifyURL = orig })
}

func TestVerify_Success(t *testing.T) {
	withFakeSiteVerify(t, func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	})
	ok, err := Verify(context.Background(), "secret", "token", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("Verify = false, want true")
	}
}

func TestVerify_RejectedToken(t *testing.T) {
	withFakeSiteVerify(t, func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}})
	})
	ok, err := Verify(context.Background(), "secret", "bad-token", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Verify = true, want false")
	}
}

func TestVerify_UpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	orig := SiteVerifyURL
	SiteVerifyURL = server.URL
	t.Cleanup(func() { SiteVerifyURL = orig })

	ok, err := Verify(context.Background(), "secret", "token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error decoding a non-JSON 500 body")
	}
	if ok {
		t.Fatal("Verify = true on upstream failure, want false (fail closed)")
	}
}
