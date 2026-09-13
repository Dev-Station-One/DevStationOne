// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Server holds configuration for the devstation-server binary.
type Server struct {
	Addr          string
	DatabaseURL   string
	LogLevel      string
	Environment   string
	AdminEmail    string
	AdminPassword string
}

// Runtime holds configuration for the devstation-runtime binary.
type Runtime struct {
	ServerURL   string
	LogLevel    string
	Environment string
}

// LoadServer reads Server config from the environment, applying defaults.
func LoadServer() (Server, error) {
	cfg := Server{
		Addr:          getenv("DEVSTATION_SERVER_ADDR", ":8080"),
		DatabaseURL:   getenv("DEVSTATION_DATABASE_URL", ""),
		LogLevel:      getenv("DEVSTATION_LOG_LEVEL", "info"),
		Environment:   getenv("DEVSTATION_ENV", "development"),
		AdminEmail:    getenv("DEVSTATION_ADMIN_EMAIL", ""),
		AdminPassword: getenv("DEVSTATION_ADMIN_PASSWORD", ""),
	}
	if cfg.DatabaseURL == "" {
		return Server{}, fmt.Errorf("DEVSTATION_DATABASE_URL is required")
	}
	return cfg, nil
}

// LoadRuntime reads Runtime config from the environment, applying defaults.
func LoadRuntime() (Runtime, error) {
	cfg := Runtime{
		ServerURL:   getenv("DEVSTATION_SERVER_URL", ""),
		LogLevel:    getenv("DEVSTATION_LOG_LEVEL", "info"),
		Environment: getenv("DEVSTATION_ENV", "development"),
	}
	if cfg.ServerURL == "" {
		return Runtime{}, fmt.Errorf("DEVSTATION_SERVER_URL is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
