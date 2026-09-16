package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

const challengeTTL = 5 * time.Minute

// ChallengePurpose distinguishes the two login-gate uses of a challenge
// (ADR-050): a challenge issued because the account has no second factor
// yet (and may enroll one to complete the login), versus one issued to
// verify an already-enrolled factor. Enrollment handlers must reject
// verify-purpose challenges — otherwise a password-possessing attacker
// could re-enroll their own authenticator over the victim's 2FA and mint a
// session from a verification-stage token.
type ChallengePurpose uint8

const (
	PurposeVerify ChallengePurpose = iota
	PurposeEnroll
)

// LoginChallenge is server-side state for a login that has passed the first
// factor (password) but not yet the second, or that must first complete
// mandatory enrollment. Held in-memory only.
//
// ponytail: single Control Server process assumption — this state does not
// survive a restart or work across multiple instances. Move to a DB-backed
// table (docs/DECISIONS.md ADR-047) only if the Control Server ever runs
// multi-instance; today's deployment is one systemd unit.
type LoginChallenge struct {
	UserID         string
	TrustRequested bool
	Purpose        ChallengePurpose
	WebAuthnData   *webauthnlib.SessionData
	// EmailCode is the one-time code most recently emailed for this
	// challenge (empty until /2fa/email/request-code is called).
	EmailCode string
	expires   time.Time
}

var (
	challengeMu    sync.Mutex
	challengeStore = map[string]*LoginChallenge{}
	challengeOnce  sync.Once
)

func startChallengeSweeper() {
	go func() {
		for range time.Tick(time.Minute) {
			challengeMu.Lock()
			now := time.Now()
			for token, c := range challengeStore {
				if now.After(c.expires) {
					delete(challengeStore, token)
				}
			}
			challengeMu.Unlock()
		}
	}()
}

// NewChallenge creates a pending login challenge for userID and returns its
// opaque token. purpose must reflect what the challenge may be used for at
// the login gate: PurposeEnroll only when the account has no confirmed
// factor yet (see api/auth.go's login handler), PurposeVerify otherwise.
func NewChallenge(userID string, trustRequested bool, purpose ChallengePurpose) (string, error) {
	challengeOnce.Do(startChallengeSweeper)

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	challengeMu.Lock()
	challengeStore[token] = &LoginChallenge{UserID: userID, TrustRequested: trustRequested, Purpose: purpose, expires: time.Now().Add(challengeTTL)}
	challengeMu.Unlock()
	return token, nil
}

// GetChallenge looks up a pending challenge by token, returning false if
// missing or expired.
func GetChallenge(token string) (LoginChallenge, bool) {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	c, ok := challengeStore[token]
	if !ok || time.Now().After(c.expires) {
		return LoginChallenge{}, false
	}
	return *c, true
}

// SetChallengeWebAuthnData attaches WebAuthn ceremony session data to an
// existing challenge, used between a begin and finish call.
func SetChallengeWebAuthnData(token string, data *webauthnlib.SessionData) bool {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	c, ok := challengeStore[token]
	if !ok || time.Now().After(c.expires) {
		return false
	}
	c.WebAuthnData = data
	return true
}

// SetChallengeEmailCode attaches a freshly emailed one-time code to an
// existing challenge, used between /2fa/email/request-code and /2fa/verify.
func SetChallengeEmailCode(token, code string) bool {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	c, ok := challengeStore[token]
	if !ok || time.Now().After(c.expires) {
		return false
	}
	c.EmailCode = code
	return true
}

// ConsumeChallenge removes and returns a challenge so its token cannot be
// replayed after a successful login.
func ConsumeChallenge(token string) (LoginChallenge, bool) {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	c, ok := challengeStore[token]
	if !ok || time.Now().After(c.expires) {
		return LoginChallenge{}, false
	}
	delete(challengeStore, token)
	return *c, true
}
