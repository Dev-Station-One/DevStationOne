// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Server holds configuration for the devstationone-server binary.
type Server struct {
	Addr        string
	DatabaseURL string
	LogLevel    string
	Environment string
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
		Addr:        getenv("DEVSTATIONONE_SERVER_ADDR", ":8080"),
		DatabaseURL: getenv("DEVSTATIONONE_DATABASE_URL", ""),
		LogLevel:    getenv("DEVSTATIONONE_LOG_LEVEL", "info"),
		Environment: getenv("DEVSTATIONONE_ENV", "development"),
	}
	if cfg.DatabaseURL == "" {
		return Server{}, fmt.Errorf("DEVSTATIONONE_DATABASE_URL is required")
	}
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
