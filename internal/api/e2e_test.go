package api

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"devstationone/internal/auth"
)

// findChromium locates a real Chrome/Chromium binary for the browser E2E
// test, skipping (not failing) when none is installed — this test proves
// the real static HTML/JS + API wire-up works end-to-end in an actual
// browser JS engine, which internal/api's httptest-based tests structurally
// cannot (they never execute JavaScript); it isn't required for the rest
// of the suite to pass.
func findChromium(t *testing.T) string {
	t.Helper()
	// google-chrome/chromium-browser first: on this class of Ubuntu install,
	// a bare "chromium" resolves to the snap wrapper (/snap/bin/chromium ->
	// /usr/bin/snap), whose confinement makes it unreliable to drive over
	// CDP from a plain exec — prefer a real ELF binary when one exists.
	for _, name := range []string{"google-chrome", "chromium-browser", "chromium"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no chromium/chrome binary found; skipping browser E2E test")
	return ""
}

// TestE2E_LoginAndActivityPage drives a real headless browser through the
// login page and the Activity page (Slice 11), proving the whole stack —
// static HTML, web/static/js/app.js, and the JSON API — actually works
// together: a login form submit, the resulting session cookie, the
// sidebar's client-side render, and a live fetch of recorded audit events.
// It builds its own router (unlike sessionsTestSetup, which points
// NewRouter's staticDir at a throwaway t.TempDir() since its tests never
// touch static HTML) so the browser has the real web/static/ to load.
func TestE2E_LoginAndActivityPage(t *testing.T) {
	chromiumPath := findChromium(t)
	db := testDB(t)
	ctx0 := context.Background()

	if _, err := auth.CreateUser(ctx0, db.Pool, "e2e-admin@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(chromiumPath),
			chromedp.NoSandbox,
			chromedp.DisableGPU,
		)...,
	)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTimeout()

	// #logout-btn exists on account.html but starts hidden inside a
	// Bootstrap dropdown-menu (no .show class until the account-menu
	// toggle is clicked) — wait on #account-email instead, which is always
	// visible on page load and is the concrete proof the account page
	// actually rendered (not just that some navigation happened).
	var pageTitle string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/login.html"),
		chromedp.WaitVisible(`#email`, chromedp.ByID),
		chromedp.SendKeys(`#email`, "e2e-admin@example.com", chromedp.ByID),
		chromedp.SendKeys(`#password`, "password123", chromedp.ByID),
		chromedp.Click(`button[type=submit]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#account-email`, chromedp.ByID),
		chromedp.Title(&pageTitle),
	)
	if err != nil {
		t.Fatalf("login flow: %v", err)
	}
	if pageTitle != "Account · DevStationOne" {
		t.Fatalf("post-login page title = %q, want Account page (login must have failed)", pageTitle)
	}

	var eventTypeText string
	err = chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/activity.html"),
		chromedp.WaitVisible(`#events-body tr`, chromedp.ByQuery),
		chromedp.Text(`#events-body tr:first-child code`, &eventTypeText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("activity page: %v", err)
	}
	// This test's own login is the most recent event by the time the
	// Activity page loads (most-recent-first ordering, internal/audit.List).
	if eventTypeText != "auth.login" {
		t.Fatalf("first activity row event_type = %q, want auth.login", eventTypeText)
	}

	var sidebarHTML string
	err = chromedp.Run(ctx,
		chromedp.Click(`button[aria-label="Toggle navigation"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`a[href="/activity.html"]`, chromedp.ByQuery),
		chromedp.OuterHTML(`#ds-sidebar`, &sidebarHTML, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("sidebar: %v", err)
	}
	if !strings.Contains(sidebarHTML, "Activity") {
		t.Fatalf("sidebar does not render the Activity nav item: %s", sidebarHTML)
	}
}
