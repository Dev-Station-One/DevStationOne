// Package turnstile verifies Cloudflare Turnstile challenge tokens
// (docs/DECISIONS.md ADR-044) submitted alongside the login form.
package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SiteVerifyURL is Cloudflare's token verification endpoint. A package
// variable, not a constant, so tests can point it at an httptest.Server
// instead of the real network — turnstile has exactly one implementation,
// so this is a test seam, not a speculative interface.
var SiteVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

const verifyTimeout = 5 * time.Second

// Verify checks token against Cloudflare's siteverify API using secretKey.
// remoteIP is the end user's IP, passed through for Cloudflare's own risk
// scoring. Any error (network, timeout, malformed response) is reported as a
// non-nil error with success=false: the caller must fail closed.
func Verify(ctx context.Context, secretKey, token, remoteIP string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	form := url.Values{
		"secret":   {secretKey},
		"response": {token},
		"remoteip": {remoteIP},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SiteVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	return result.Success, nil
}
