// Package push delivers per-user notifications to browsers over Web Push
// (RFC 8030/8291/8292) so a closed or backgrounded tab still alerts
// (docs/DECISIONS.md ADR-055). It taps the events.Bus: every
// "notification.created" event already recorded by internal/notifications is
// forwarded to that user's registered push subscriptions.
package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"devstationone/internal/events"
	"devstationone/internal/notifications"
)

const (
	sendTimeout = 15 * time.Second
	// ttlSeconds is how long a push service holds a message for an offline
	// browser; a stale "task finished" alert is noise after a day.
	ttlSeconds = 86400
)

// Service owns the installation's VAPID keypair and sends pushes.
type Service struct {
	pool       *pgxpool.Pool
	logger     *slog.Logger
	subject    string
	publicKey  string
	privateKey string
	client     webpush.HTTPClient
}

// New loads the installation's VAPID keypair, generating and storing it on
// first use. subject is the VAPID "sub" claim (an https: or mailto: URL
// push services may use to contact the operator).
func New(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, encKey []byte, subject string) (*Service, error) {
	s := &Service{pool: pool, logger: logger, subject: subject, client: &http.Client{Timeout: sendTimeout}}

	var enc []byte
	err := pool.QueryRow(ctx, `SELECT public_key, private_key_enc FROM push_vapid`).Scan(&s.publicKey, &enc)
	if errors.Is(err, pgx.ErrNoRows) {
		priv, pub, gerr := webpush.GenerateVAPIDKeys()
		if gerr != nil {
			return nil, fmt.Errorf("generate vapid keys: %w", gerr)
		}
		sealed, gerr := seal(encKey, []byte(priv))
		if gerr != nil {
			return nil, gerr
		}
		// ON CONFLICT: two servers starting together converge on one row.
		if _, gerr = pool.Exec(ctx, `INSERT INTO push_vapid (public_key, private_key_enc) VALUES ($1, $2) ON CONFLICT DO NOTHING`, pub, sealed); gerr != nil {
			return nil, fmt.Errorf("store vapid keys: %w", gerr)
		}
		err = pool.QueryRow(ctx, `SELECT public_key, private_key_enc FROM push_vapid`).Scan(&s.publicKey, &enc)
	}
	if err != nil {
		return nil, fmt.Errorf("load vapid keys: %w", err)
	}
	priv, err := open(encKey, enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt vapid private key (wrong DEVSTATIONONE_DB_ENCRYPTION_KEY?): %w", err)
	}
	s.privateKey = string(priv)
	return s, nil
}

// PublicKey is the base64url VAPID public key browsers pass to
// pushManager.subscribe as applicationServerKey.
func (s *Service) PublicKey() string { return s.publicKey }

// Attach forwards every "notification.created" event on bus to the owning
// user's push subscriptions. Delivery runs on its own goroutine so a slow
// push service never blocks the publisher.
func (s *Service) Attach(bus *events.Bus) {
	bus.Tap(func(e events.Event) {
		if e.Type != "notification.created" {
			return
		}
		n, ok := e.Payload.(notifications.Notification)
		if !ok {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*sendTimeout)
			defer cancel()
			s.Notify(ctx, n)
		}()
	})
}

// payload is the JSON the service worker receives. Deliberately small: the
// title/body of an in-app notification, a deep link, and a tag so a repeat
// of the same notification replaces rather than stacks.
type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// Notify sends n to every subscription of n.UserID. Subscriptions the push
// service reports gone (404/410) are deleted. Best-effort: failures are
// logged, never returned, matching notifications.Record.
func (s *Service) Notify(ctx context.Context, n notifications.Notification) {
	body, err := json.Marshal(payload{Title: n.Title, Body: n.Body, URL: deepLink(n), Tag: n.ID})
	if err != nil {
		s.logger.Error("push: marshal payload", "error", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1`, n.UserID)
	if err != nil {
		s.logger.Error("push: list subscriptions", "error", err)
		return
	}
	type sub struct{ id, endpoint, p256dh, auth string }
	var subs []sub
	for rows.Next() {
		var x sub
		if err := rows.Scan(&x.id, &x.endpoint, &x.p256dh, &x.auth); err != nil {
			rows.Close()
			s.logger.Error("push: scan subscription", "error", err)
			return
		}
		subs = append(subs, x)
	}
	rows.Close()

	for _, x := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
			Endpoint: x.endpoint,
			Keys:     webpush.Keys{P256dh: x.p256dh, Auth: x.auth},
		}, &webpush.Options{
			HTTPClient:      s.client,
			Subscriber:      s.subject,
			VAPIDPublicKey:  s.publicKey,
			VAPIDPrivateKey: s.privateKey,
			TTL:             ttlSeconds,
		})
		if err != nil {
			// The error text can embed the endpoint URL (a capability), so
			// log only the subscription id.
			s.logger.Warn("push: send failed", "subscription", x.id)
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			if _, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, x.id); err != nil {
				s.logger.Error("push: delete expired subscription", "error", err)
			}
		case resp.StatusCode >= 300:
			s.logger.Warn("push: service rejected message", "subscription", x.id, "status", resp.StatusCode)
		}
	}
}

// deepLink is where a click on the notification should land.
func deepLink(n notifications.Notification) string {
	if id, ok := n.Detail["project_id"].(string); ok && id != "" {
		return "/project.html?id=" + url.QueryEscape(id)
	}
	return "/projects.html"
}

// Subscribe stores (or re-homes) one browser push subscription for userID.
func (s *Service) Subscribe(ctx context.Context, userID, endpoint, p256dh, auth, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE
		SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent`,
		userID, endpoint, p256dh, auth, userAgent)
	return err
}

// Unsubscribe removes userID's subscription for endpoint. Idempotent.
func (s *Service) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, userID, endpoint)
	return err
}

// ValidEndpoint rejects subscription endpoints the server must not POST to:
// the server fetches whatever URL a client registers, so it must be a public
// https URL (real push services — FCM, Mozilla autopush, Apple, WNS — are
// all public https hosts). IP literals in private/loopback ranges and
// "localhost" are refused; DNS names resolving to private space are not
// checked (docs/DECISIONS.md ADR-055).
func ValidEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("endpoint must be an https URL")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("endpoint host not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return errors.New("endpoint host not allowed")
	}
	return nil
}

func seal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func open(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}
