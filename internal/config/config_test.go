package config

import (
	"os"
	"testing"
)

func TestLoadServer_DefaultsDatabaseURL(t *testing.T) {
	orig, had := os.LookupEnv("DEVSTATIONONE_DATABASE_URL")
	os.Unsetenv("DEVSTATIONONE_DATABASE_URL")
	t.Cleanup(func() {
		if had {
			os.Setenv("DEVSTATIONONE_DATABASE_URL", orig)
		}
	})

	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "postgres://devstation:devstation@localhost:55432/devstation?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestLoadServer_Defaults(t *testing.T) {
	t.Setenv("DEVSTATIONONE_DATABASE_URL", "postgres://example")
	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
}

func TestLoadRuntime_RequiresServerURL(t *testing.T) {
	t.Setenv("DEVSTATIONONE_SERVER_URL", "")
	t.Setenv("DEVSTATIONONE_RUNTIME_TOKEN", "sometoken")
	if _, err := LoadRuntime(); err == nil {
		t.Fatal("expected error when DEVSTATIONONE_SERVER_URL is unset")
	}
}

func TestLoadRuntime_RequiresToken(t *testing.T) {
	t.Setenv("DEVSTATIONONE_SERVER_URL", "https://example.com")
	t.Setenv("DEVSTATIONONE_RUNTIME_TOKEN", "")
	if _, err := LoadRuntime(); err == nil {
		t.Fatal("expected error when DEVSTATIONONE_RUNTIME_TOKEN is unset")
	}
}
