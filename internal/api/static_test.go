package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRouter_StaticSource covers the ADR-048 dispatch in NewRouter: ""
// serves the binary's embedded web.StaticFS/assets.FS (the production
// default — proves the server is self-contained with no web/static or
// assets directory on disk), any other value still serves that path from
// disk unchanged (DEVSTATIONONE_DEV_STATIC).
func TestNewRouter_StaticSource(t *testing.T) {
	db := testDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("embedded", func(t *testing.T) {
		router, _, _, _, _ := NewRouter(db, logger, "", testDBEncryptionKey, "", "", TwoFactorConfig{})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("embedded index.html body missing <html")
		}

		req = httptest.NewRequest(http.MethodGet, "/assets/DevStationOne-OG-Social-Preview.svg", nil)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /assets/DevStationOne-OG-Social-Preview.svg status = %d, want 200", rec.Code)
		}
	})

	t.Run("disk", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "marker.html"), []byte("disk-marker"), 0o644); err != nil {
			t.Fatal(err)
		}
		router, _, _, _, _ := NewRouter(db, logger, dir, testDBEncryptionKey, "", "", TwoFactorConfig{})

		req := httptest.NewRequest(http.MethodGet, "/marker.html", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "disk-marker" {
			t.Fatalf("GET /marker.html = %d %q, want 200 %q", rec.Code, rec.Body.String(), "disk-marker")
		}
	})
}
