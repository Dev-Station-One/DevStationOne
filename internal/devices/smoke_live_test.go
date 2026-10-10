package devices

// Live smoke against the real dev Control Server; run manually with:
//   DEVSTATION_LIVE_SMOKE_URL=http://192.168.25.165:18091 DEVSTATION_LIVE_SMOKE_CODE=<code> \
//   go test ./internal/devices/ -run TestLiveSmoke -v
// (pairing code must be minted first via `devstationone-admin pair`).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveSmoke(t *testing.T) {
	base := os.Getenv("DEVSTATION_LIVE_SMOKE_URL")
	code := os.Getenv("DEVSTATION_LIVE_SMOKE_CODE")
	if base == "" || code == "" {
		t.Skip("live smoke env not set")
	}
	client := &http.Client{}
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Pair over the real endpoint.
	body := `{"code":"` + code + `","public_key":"` + key.PublicB64 + `"}`
	pr, err := client.Do(mustReq(t, http.MethodPost, base+"/api/v1/auth/devices/pair", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	pb, _ := io.ReadAll(pr.Body)
	pr.Body.Close()
	if pr.StatusCode != 201 {
		t.Fatalf("pair status %d: %s", pr.StatusCode, pb)
	}
	var paired struct {
		ID string `json:"id"`
	}
	json.Unmarshal(pb, &paired)
	if paired.ID == "" {
		t.Fatalf("pair response had no id: %s", pb)
	}
	t.Logf("paired device %s", paired.ID)

	// Signed GET /api/v1/projects — the identity path the app will use.
	sres := signedReq(t, client, key, paired.ID, http.MethodGet, base, "/api/v1/projects")
	if sres.StatusCode != 200 {
		t.Fatalf("signed projects status %d", sres.StatusCode)
	}
	sres.Body.Close()

	// Signed device list + revoke.
	sres = signedReq(t, client, key, paired.ID, http.MethodGet, base, "/api/v1/auth/devices")
	if sres.StatusCode != 200 {
		t.Fatalf("signed devices status %d", sres.StatusCode)
	}
	sres.Body.Close()
	uri := "/api/v1/auth/devices/" + paired.ID
	dres := signedReq(t, client, key, paired.ID, http.MethodDelete, base, uri)
	dres.Body.Close()
	if dres.StatusCode != 204 {
		t.Fatalf("revoke status %d", dres.StatusCode)
	}

	// Post-revoke signed request must 401.
	sres = signedReq(t, client, key, paired.ID, http.MethodGet, base, "/api/v1/auth/devices")
	if sres.StatusCode != 401 {
		t.Fatalf("post-revoke status %d, want 401", sres.StatusCode)
	}
	sres.Body.Close()
}

func signedReq(t *testing.T, c *http.Client, key KeyPair, deviceID, method, base, uri string) *http.Response {
	t.Helper()
	hdr, err := Sign(key, deviceID, method, hostOf(base), uri, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(method, base+uri, http.NoBody)
	req.Header.Set("Authorization", hdr)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func hostOf(base string) string {
	u, _ := url.Parse(base)
	return u.Host
}
