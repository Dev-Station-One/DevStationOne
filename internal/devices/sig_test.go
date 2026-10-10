package devices

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignParseRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Unix(1730000000, 0)
	h, err := Sign(key, "d-1", "GET", "example.com", "/api/v1/projects?limit=5", now)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parsed, ok := ParseSigHeader(h)
	if !ok {
		t.Fatalf("ParseSigHeader rejected %q", h)
	}
	if parsed.ID != "d-1" || parsed.TS != "1730000000" || parsed.Nonce == "" || len(parsed.Signature) == 0 {
		t.Fatalf("bad parse: %+v", parsed)
	}
	if len(parsed.Nonce) != 16 { // 12 bytes raw-url-encoded
		t.Fatalf("nonce length %d, want 16 base64url chars", len(parsed.Nonce))
	}
}

func TestParseSigHeaderRejects(t *testing.T) {
	bad := []string{
		"",
		"Bearer x",
		"Sig",
		"Sig id=\"d\", ts=\"1\", nonce=\"n\"", // no sig
		`Sig id="d", ts="1", nonce="n", sig="not-base64!"`,
		`Sig id="d", ts="1", nonce="n", sig="AAAA", foo="bar"`,
		`Sig ts="1", nonce="n", sig="AAAA"`,
	}
	for _, h := range bad {
		if _, ok := ParseSigHeader(h); ok {
			t.Fatalf("ParseSigHeader accepted %q", h)
		}
	}
}

func TestNonceCacheMarkAndFresh(t *testing.T) {
	var c nonceCache
	if !c.fresh("n1") || !c.fresh("n1") {
		t.Fatal("fresh before mark should always be true")
	}
	c.mark("n1", time.Now().Add(time.Hour))
	if c.fresh("n1") {
		t.Fatal("marked nonce should not be fresh")
	}
	if !c.fresh("n2") {
		t.Fatal("unrelated nonce should stay fresh")
	}
	// Expired marked nonces stop being duplicates (mark's amortized prune
	// drops them once >=1000 inserts have passed).
	c.mark("e1", time.Now().Add(-2*clockWindow))
	for i := 0; i < 1000; i++ {
		c.mark("nonce-"+strconv.Itoa(i), time.Now())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.seen["e1"]; dup {
		t.Fatal("expired nonce survived prune")
	}
	if _, dup := c.seen["nonce-999"]; !dup {
		t.Fatal("prune deleted live nonces")
	}
}

func TestSignIsPerRequestUnique(t *testing.T) {
	key, _ := GenerateKey()
	a, _ := Sign(key, "d", "GET", "h", "/x", time.Now())
	b, _ := Sign(key, "d", "GET", "h", "/x", time.Now())
	if a == b {
		t.Fatal("two signatures identical — nonce or ts did not vary")
	}
}

// The signed message covers METHOD: a signature made for one method does
// not verify against another method's message.
func TestMessageCoversMethod(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := key.Private.PublicKey
	der, _ := x509.MarshalPKIXPublicKey(&pub)
	spki, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("parse SPKI: %v", err)
	}

	ts, nonce := "1730000000", "AAAAAAAAAAAAAAAA"
	sig, _ := ecdsa.SignASN1(rand.Reader, key.Private, []byte(Message("GET", "h", "/x", ts, nonce)))
	if !ecdsa.VerifyASN1(spki.(*ecdsa.PublicKey), []byte(Message("GET", "h", "/x", ts, nonce)), sig) {
		t.Fatal("signature should verify for its own method")
	}
	if ecdsa.VerifyASN1(spki.(*ecdsa.PublicKey), []byte(Message("DELETE", "h", "/x", ts, nonce)), sig) {
		t.Fatal("signature verified for a different method — METHOD not covered")
	}
	msg := Message("GET", "h", "/x", ts, nonce)
	if ecdsa.VerifyASN1(spki.(*ecdsa.PublicKey), []byte(strings.Replace(msg, "/x", "/y", 1)), sig) {
		t.Fatal("signature verified for a different URI — URI not covered")
	}
}
