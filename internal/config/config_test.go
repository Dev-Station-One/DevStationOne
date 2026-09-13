package config

import "testing"

func TestLoadServer_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DEVSTATION_DATABASE_URL", "")
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected error when DEVSTATION_DATABASE_URL is unset")
	}
}

func TestLoadServer_Defaults(t *testing.T) {
	t.Setenv("DEVSTATION_DATABASE_URL", "postgres://example")
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
	t.Setenv("DEVSTATION_SERVER_URL", "")
	if _, err := LoadRuntime(); err == nil {
		t.Fatal("expected error when DEVSTATION_SERVER_URL is unset")
	}
}
