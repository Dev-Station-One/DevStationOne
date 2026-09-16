package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"devstationone/internal/auth"
)

// browseTestSetup registers and connects a runtime rooted at a dedicated
// temp home directory, without creating a Project — matching the Project
// setup UI's directory picker, which runs before a Project exists.
func browseTestSetup(t *testing.T) (router http.Handler, cookie *http.Cookie, runtimeID, homeDir string) {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()

	if _, err := auth.CreateUser(ctx, db.Pool, "admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ = NewRouter(db, logger, t.TempDir(), testDBEncryptionKey, "", "", TwoFactorConfig{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	cookie = loginAs(t, router, "admin@example.com", "password123")

	cur, err := user.Current()
	if err != nil {
		t.Fatalf("determine current OS user: %v", err)
	}

	homeDir = t.TempDir()

	body, _ := json.Marshal(map[string]string{"name": "dev01"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created runtimeWithTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}
	runtimeID = created.ID

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startFakeRuntime(t, runCtx, server.URL, created.Token, cur.Username, homeDir, []string{"/bin/sh"})

	pollUntil(t, 3*time.Second, func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var list []runtimeResponse
		json.NewDecoder(rec.Body).Decode(&list)
		for _, rt := range list {
			if rt.ID == runtimeID && rt.Online {
				return true
			}
		}
		return false
	})

	return router, cookie, runtimeID, homeDir
}

func TestRuntimeBrowseHTTP_Search(t *testing.T) {
	router, cookie, runtimeID, homeDir := browseTestSetup(t)

	if err := os.Mkdir(filepath.Join(homeDir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "sub", "alpha-beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "alpha.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/"+runtimeID+"/browse/search?q=alpha&path=.", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var matches []string
	if err := json.NewDecoder(rec.Body).Decode(&matches); err != nil {
		t.Fatalf("decode matches: %v", err)
	}
	want := map[string]bool{"alpha": true, "sub/alpha-beta": true}
	if len(matches) != len(want) {
		t.Fatalf("search matches = %v, want keys of %v", matches, want)
	}
	for _, m := range matches {
		if !want[m] {
			t.Errorf("unexpected match %q (dirs_only should exclude alpha.txt)", m)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/"+runtimeID+"/browse/search?path=.", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("search without q: status = %d, want 400", rec.Code)
	}
}
