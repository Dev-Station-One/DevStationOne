package api

import (
	"net/http/httptest"
	"testing"
)

// TestClientIPTrustedProxy exercises X-Forwarded-For handling: honored only
// from a configured trusted proxy (DEVSTATIONONE_TRUSTED_PROXIES), ignored
// from an untrusted peer or when no proxies are configured.
func TestClientIPTrustedProxy(t *testing.T) {
	SetTrustedProxies("10.0.0.0/8, 192.168.25.165")
	t.Cleanup(func() { SetTrustedProxies("") })

	// Trusted proxy peer: the leftmost valid XFF entry wins.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:45678"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.1.2.3")
	if got := clientIP(req); got != "203.0.113.7" {
		t.Fatalf("behind trusted proxy = %q, want 203.0.113.7", got)
	}

	// Untrusted peer: the header must be ignored (no spoofing).
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:45678"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(req); got != "203.0.113.9" {
		t.Fatalf("untrusted peer = %q, want 203.0.113.9", got)
	}

	// Trusted proxy, no header: fall back to the peer address.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:45678"
	if got := clientIP(req); got != "10.1.2.3" {
		t.Fatalf("no XFF header = %q, want 10.1.2.3", got)
	}

	// Empty config (direct deployment): header ignored.
	SetTrustedProxies("")
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:45678"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(req); got != "10.1.2.3" {
		t.Fatalf("empty trusted proxies = %q, want 10.1.2.3", got)
	}

	// Single-IP (non-CIDR) config form; unparseable XFF entries are skipped.
	SetTrustedProxies("192.168.25.165")
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.25.165:443"
	req.Header.Set("X-Forwarded-For", "not-an-ip, 198.51.100.7")
	if got := clientIP(req); got != "198.51.100.7" {
		t.Fatalf("leftmost valid hop = %q, want 198.51.100.7", got)
	}
}
