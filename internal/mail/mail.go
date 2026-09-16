// Package mail sends outbound account email (2FA one-time codes, account
// recovery links) over SMTP using the stdlib client.
package mail

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Config holds SMTP server settings and the From address. A zero Host means
// mail sending is disabled (docs/SECURITY.md: credentials never logged).
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Enabled reports whether SMTP is configured.
func (c Config) Enabled() bool { return c.Host != "" }

// Send delivers a plain-text email. to and subject must not contain CR/LF
// (header injection guard); body is sent as-is.
func Send(cfg Config, to, subject, body string) error {
	if !cfg.Enabled() {
		return fmt.Errorf("smtp not configured")
	}
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") {
		return fmt.Errorf("invalid header value")
	}

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		cfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg))
}
