package config

import (
	"os"
	"testing"
)

// testEncryptionKey is 32 raw bytes, base64-encoded — a valid
// DEVSTATIONONE_DB_ENCRYPTION_KEY shape for tests that don't exercise the key
// itself.
const testEncryptionKey = "AXnPprXAcRUTnm6lHFbayv5MBmW+M0kUhbxUY5XoTzI="

func TestLoadServer_DefaultsDatabaseURL(t *testing.T) {
	orig, had := os.LookupEnv("DEVSTATIONONE_DATABASE_URL")
	os.Unsetenv("DEVSTATIONONE_DATABASE_URL")
	t.Cleanup(func() {
		if had {
			os.Setenv("DEVSTATIONONE_DATABASE_URL", orig)
		}
	})
	t.Setenv("DEVSTATIONONE_DB_ENCRYPTION_KEY", testEncryptionKey)

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
	t.Setenv("DEVSTATIONONE_DB_ENCRYPTION_KEY", testEncryptionKey)
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
	if len(cfg.DBEncryptionKey) != 32 {
		t.Errorf("DBEncryptionKey length = %d, want 32", len(cfg.DBEncryptionKey))
	}
	if cfg.IdleNotifySeconds != 120 {
		t.Errorf("IdleNotifySeconds = %d, want 120", cfg.IdleNotifySeconds)
	}
}

func TestLoadServer_IdleNotifySecondsOverride(t *testing.T) {
	t.Setenv("DEVSTATIONONE_DATABASE_URL", "postgres://example")
	t.Setenv("DEVSTATIONONE_DB_ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("DEVSTATIONONE_IDLE_NOTIFY_SECONDS", "60")
	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IdleNotifySeconds != 60 {
		t.Errorf("IdleNotifySeconds = %d, want 60", cfg.IdleNotifySeconds)
	}
}

func TestLoadServer_RequiresEncryptionKey(t *testing.T) {
	t.Setenv("DEVSTATIONONE_DATABASE_URL", "postgres://example")
	t.Setenv("DEVSTATIONONE_DB_ENCRYPTION_KEY", "")
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected error when DEVSTATIONONE_DB_ENCRYPTION_KEY is unset")
	}
}

func TestLoadServer_RejectsBadEncryptionKeyLength(t *testing.T) {
	t.Setenv("DEVSTATIONONE_DATABASE_URL", "postgres://example")
	t.Setenv("DEVSTATIONONE_DB_ENCRYPTION_KEY", "dG9vc2hvcnQ=") // "tooshort", not 32 bytes
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected error for a non-32-byte encryption key")
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
