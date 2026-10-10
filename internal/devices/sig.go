package devices

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/auth"
)

// The signature scheme (ADR-053), mirrored by the Android client:
//
//	Authorization: Sig id="<device id>", ts="<unix>", nonce="<b64url>", sig="<b64 DER>"
//	signed over "devstationone-v1\nMETHOD\nHOST\nREQUEST_URI\nts\nnonce"
//
// Timestamps must be within 60 s in both directions (phone clock discipline,
// same bound muxalot pairs against) and each nonce is single-use, so a
// captured Authorization header cannot be replayed.

const (
	Scheme     = "Sig"
	sigVersion = "devstationone-v1"
	// clockWindow bounds acceptable ts skew in both directions.
	clockWindow = 60 * time.Second
)

// Message builds the exact byte string a request must be signed over.
func Message(method, host, requestURI, ts, nonce string) string {
	return strings.Join([]string{sigVersion, method, host, requestURI, ts, nonce}, "\n")
}

// SigHeader is one parsed Authorization header.
type SigHeader struct {
	ID        string
	TS        string
	Nonce     string
	Signature []byte
}

// ParseSigHeader parses the Authorization header's Sig scheme.
func ParseSigHeader(h string) (SigHeader, bool) {
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || parts[0] != Scheme {
		return SigHeader{}, false
	}
	var out SigHeader
	for _, kv := range strings.Split(parts[1], ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return SigHeader{}, false
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "id":
			out.ID = v
		case "ts":
			out.TS = v
		case "nonce":
			out.Nonce = v
		case "sig":
			sig, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return SigHeader{}, false
			}
			out.Signature = sig
		default:
			return SigHeader{}, false
		}
	}
	if out.ID == "" || out.TS == "" || out.Nonce == "" || len(out.Signature) == 0 {
		return SigHeader{}, false
	}
	return out, true
}

// nonceCache is the replay guard: each nonce works once and is forgotten
// after the ts window has passed it.
//
// ponytail: in-memory, per-process (matches runtimes.Registry — ADR-012).
// After a Control Server restart the 60 s window is the only replay bound
// for those seconds; acceptable at self-hosted scale.
type nonceCache struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	since int // inserts since the last prune
}

func (c *nonceCache) mark(nonce string, expiry time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]time.Time)
	}
	c.since++
	if c.since >= 1000 { // ponytail: amortized prune, no timer goroutine
		for k, t := range c.seen {
			if time.Now().After(t) {
				delete(c.seen, k)
			}
		}
		c.since = 0
	}
	c.seen[nonce] = expiry
}

func (c *nonceCache) fresh(nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, dup := c.seen[nonce]
	return !dup
}

// Authenticator verifies signed requests against the devices table.
type Authenticator struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	rn     nonceCache
}

// NewAuthenticator builds the request verifier for one Control Server.
func NewAuthenticator(pool *pgxpool.Pool, logger *slog.Logger) *Authenticator {
	return &Authenticator{pool: pool, logger: logger}
}

// Authenticate verifies the Sig header of one request and returns the
// device and its owning user. r.RequestURI must be the same string the
// client signed — behind a TLS proxy that rewritten Host/URI will fail
// verification (documents/API.md "device authentication").
func (a *Authenticator) Authenticate(ctx context.Context, r *http.Request) (Device, auth.User, error) {
	h, ok := ParseSigHeader(r.Header.Get("Authorization"))
	if !ok {
		a.logger.Warn("device auth rejected", "reason", "malformed Sig header")
		return Device{}, auth.User{}, fmt.Errorf("malformed Sig header")
	}

	ts, err := strconv.ParseInt(h.TS, 10, 64)
	if err != nil {
		a.logger.Warn("device auth rejected", "reason", "bad ts")
		return Device{}, auth.User{}, fmt.Errorf("bad ts")
	}
	skew := time.Since(time.Unix(ts, 0))
	if skew > clockWindow || skew < -clockWindow {
		a.logger.Warn("device auth rejected", "reason", "ts skew", "skew", skew.String())
		return Device{}, auth.User{}, fmt.Errorf("timestamp skew %s exceeds %s", skew, clockWindow)
	}
	if !a.rn.fresh(h.Nonce) {
		a.logger.Warn("device auth rejected", "reason", "replayed nonce")
		return Device{}, auth.User{}, fmt.Errorf("replayed nonce")
	}

	d, err := Get(ctx, a.pool, h.ID)
	if err != nil {
		a.logger.Warn("device auth rejected", "reason", "device lookup", "device", h.ID, "err", err)
		return Device{}, auth.User{}, err
	}
	if d.RevokedAt != nil {
		a.logger.Warn("device auth rejected", "reason", "device revoked", "device", h.ID)
		return Device{}, auth.User{}, fmt.Errorf("device revoked")
	}

	der, err := base64.StdEncoding.DecodeString(d.PublicKey)
	if err != nil {
		a.logger.Warn("device auth rejected", "reason", "pubkey not base64", "device", d.ID)
		return Device{}, auth.User{}, fmt.Errorf("stored pubkey not base64: %w", err)
	}
	anyKey, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		a.logger.Warn("device auth rejected", "reason", "pubkey not SPKI", "device", d.ID)
		return Device{}, auth.User{}, fmt.Errorf("stored pubkey not SPKI: %w", err)
	}
	key, ok := anyKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		a.logger.Warn("device auth rejected", "reason", "pubkey not P-256", "device", d.ID)
		return Device{}, auth.User{}, fmt.Errorf("stored pubkey not ECDSA P-256")
	}
	msg := Message(r.Method, r.Host, r.RequestURI, h.TS, h.Nonce)
	// Signatures verify over the SHA-256 digest of the signed message —
	// the Android Keystore's SHA256withECDSA hashes first, so the Go side
	// must too (ecdsa.VerifyASN1 takes a digest, not the raw message).
	digest := sha256.Sum256([]byte(msg))
	if !ecdsa.VerifyASN1(key, digest[:], h.Signature) {
		a.logger.Warn("device auth rejected", "reason", "signature mismatch", "device", d.ID,
			"method", r.Method, "host", r.Host, "uri", r.RequestURI, "ts", h.TS,
			"msg_hex", fmt.Sprintf("%x", msg), "sig_len", len(h.Signature), "sig_hex", fmt.Sprintf("%x", h.Signature))
		return Device{}, auth.User{}, fmt.Errorf("signature mismatch")
	}

	// Only burn the nonce for a fully verified request, so a tampered probe
	// can't evict a live phone's nonce (a fresh nonce per request anyway,
	// but correctness is free here).
	a.rn.mark(h.Nonce, time.Unix(ts, 0).Add(clockWindow).Add(clockWindow))
	Touch(context.WithoutCancel(ctx), a.pool, d.ID)

	user, err := auth.GetUserByID(ctx, a.pool, d.UserID)
	if err != nil {
		return Device{}, auth.User{}, err
	}
	return d, user, nil
}

// KeyPair is a generated test/helper key pair; PublicKey is the base64 SPKI
// form stored in the devices table and sent to the server at pairing.
type KeyPair struct {
	Private   *ecdsa.PrivateKey
	PublicB64 string
}

// GenerateKey produces a new P-256 device key pair.
func GenerateKey() (KeyPair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Private: priv, PublicB64: base64.StdEncoding.EncodeToString(der)}, nil
}

// Sign builds the Authorization header value for one request.
func Sign(key KeyPair, deviceID, method, host, requestURI string, now time.Time) (string, error) {
	ts := strconv.FormatInt(now.Unix(), 10)
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	// ecdsa.SignASN1 takes the digest — hash exactly like the Android
	// Keystore's SHA256withECDSA does.
	digest := sha256.Sum256([]byte(Message(method, host, requestURI, ts, nonceB64)))
	der, err := ecdsa.SignASN1(rand.Reader, key.Private, digest[:])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`Sig id="%s", ts="%s", nonce="%s", sig="%s"`,
		deviceID, ts, nonceB64, base64.StdEncoding.EncodeToString(der)), nil
}
