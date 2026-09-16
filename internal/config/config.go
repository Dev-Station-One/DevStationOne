// Package config loads process configuration from environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
)

// Server holds configuration for the devstationone-server binary.
type Server struct {
	Addr               string
	DatabaseURL        string
	LogLevel           string
	Environment        string
	DBEncryptionKey    []byte
	IdleNotifySeconds  int
	TurnstileSiteKey   string
	TurnstileSecretKey string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPassword       string
	SMTPFrom           string
	SMTPStream         string
	WebAuthnRPID       string
	WebAuthnRPOrigin   string
	DevStatic          bool
}

// Runtime holds configuration for the devstationone-runtime binary.
type Runtime struct {
	ServerURL   string
	Token       string
	LogLevel    string
	Environment string
}

// LoadServer reads Server config from the environment, applying defaults.
func LoadServer() (Server, error) {
	cfg := Server{
		Addr:               getenv("DEVSTATIONONE_SERVER_ADDR", ":8080"),
		DatabaseURL:        getenv("DEVSTATIONONE_DATABASE_URL", "postgres://devstation:devstation@localhost:55432/devstation?sslmode=disable"),
		LogLevel:           getenv("DEVSTATIONONE_LOG_LEVEL", "info"),
		Environment:        getenv("DEVSTATIONONE_ENV", "development"),
		IdleNotifySeconds:  getenvInt("DEVSTATIONONE_IDLE_NOTIFY_SECONDS", 120),
		TurnstileSiteKey:   getenv("DEVSTATIONONE_TURNSTILE_SITE_KEY", ""),
		TurnstileSecretKey: getenv("DEVSTATIONONE_TURNSTILE_SECRET_KEY", ""),
		SMTPHost:           getenv("DEVSTATIONONE_SMTP_HOST", ""),
		SMTPPort:           getenvInt("DEVSTATIONONE_SMTP_PORT", 587),
		SMTPUsername:       getenv("DEVSTATIONONE_SMTP_USERNAME", ""),
		SMTPPassword:       getenv("DEVSTATIONONE_SMTP_PASSWORD", ""),
		SMTPFrom:           getenv("DEVSTATIONONE_SMTP_FROM", ""),
		SMTPStream:         getenv("DEVSTATIONONE_SMTP_STREAM", ""),
		WebAuthnRPID:       getenv("DEVSTATIONONE_WEBAUTHN_RP_ID", "localhost"),
		WebAuthnRPOrigin:   getenv("DEVSTATIONONE_WEBAUTHN_RP_ORIGIN", "http://localhost:8080"),
		DevStatic:          getenv("DEVSTATIONONE_DEV_STATIC", "") != "",
	}
	if (cfg.TurnstileSiteKey == "") != (cfg.TurnstileSecretKey == "") {
		return Server{}, fmt.Errorf("DEVSTATIONONE_TURNSTILE_SITE_KEY and DEVSTATIONONE_TURNSTILE_SECRET_KEY must both be set, or both unset")
	}
	if cfg.SMTPHost == "" && (cfg.SMTPUsername != "" || cfg.SMTPPassword != "" || cfg.SMTPFrom != "") {
		return Server{}, fmt.Errorf("DEVSTATIONONE_SMTP_HOST is required when other DEVSTATIONONE_SMTP_* values are set")
	}
	keyB64 := os.Getenv("DEVSTATIONONE_DB_ENCRYPTION_KEY")
	if keyB64 == "" {
		return Server{}, fmt.Errorf("DEVSTATIONONE_DB_ENCRYPTION_KEY is required (generate with: openssl rand -base64 32)")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return Server{}, fmt.Errorf("DEVSTATIONONE_DB_ENCRYPTION_KEY must be base64: %w", err)
	}
	if len(key) != 32 {
		return Server{}, fmt.Errorf("DEVSTATIONONE_DB_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.DBEncryptionKey = key
	return cfg, nil
}

// LoadRuntime reads Runtime config from the environment, applying defaults.
func LoadRuntime() (Runtime, error) {
	cfg := Runtime{
		ServerURL:   getenv("DEVSTATIONONE_SERVER_URL", ""),
		Token:       getenv("DEVSTATIONONE_RUNTIME_TOKEN", ""),
		LogLevel:    getenv("DEVSTATIONONE_LOG_LEVEL", "info"),
		Environment: getenv("DEVSTATIONONE_ENV", "development"),
	}
	if cfg.ServerURL == "" {
		return Runtime{}, fmt.Errorf("DEVSTATIONONE_SERVER_URL is required")
	}
	if cfg.Token == "" {
		return Runtime{}, fmt.Errorf("DEVSTATIONONE_RUNTIME_TOKEN is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
