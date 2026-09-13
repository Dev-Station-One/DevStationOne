package config

import "testing"

func TestLoadServer_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DEVSTATIONONE_DATABASE_URL", "")
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected error when DEVSTATIONONE_DATABASE_URL is unset")
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
