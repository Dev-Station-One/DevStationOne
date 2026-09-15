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
	router, _, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey)
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

	// Login lands on the dashboard (index.html), not account.html. Wait on
	// #dash-runtimes-empty instead of a generic element: it only becomes
	// visible once the dashboard's own JS has actually fetched
	// /api/v1/runtimes and found none (this test registers no runtime),
	// concrete proof the dashboard rendered and ran, not just that some
	// navigation happened.
	var pageTitle string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/login.html"),
		chromedp.WaitVisible(`#email`, chromedp.ByID),
		chromedp.SendKeys(`#email`, "e2e-admin@example.com", chromedp.ByID),
		chromedp.SendKeys(`#password`, "password123", chromedp.ByID),
		chromedp.Click(`button[type=submit]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#dash-runtimes-empty`, chromedp.ByID),
		chromedp.Title(&pageTitle),
	)
	if err != nil {
		t.Fatalf("login flow: %v", err)
	}
	if pageTitle != "DevStationOne" {
		t.Fatalf("post-login page title = %q, want Dashboard page (login must have failed)", pageTitle)
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

// TestE2E_HelpSystem drives a real headless browser through the help
// manual (web/static/help.html + web/static/help/*.md, rendered client-side
// via marked/DOMPurify) and a contextual popover (web/static/js/help.js,
// backed by the vendored Bootstrap Popover chain in
// web/static/vendor/bootstrap/js/popover/). Both are pure static assets —
// no API/DB involvement — so this is the only kind of test that can prove
// the frontmatter fetch/parse and popover wiring actually work together in
// a real browser.
func TestE2E_HelpSystem(t *testing.T) {
	chromiumPath := findChromium(t)
	db := testDB(t)
	ctx0 := context.Background()

	if _, err := auth.CreateUser(ctx0, db.Pool, "e2e-help@example.com", "password123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey)
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

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/login.html"),
		chromedp.WaitVisible(`#email`, chromedp.ByID),
		chromedp.SendKeys(`#email`, "e2e-help@example.com", chromedp.ByID),
		chromedp.SendKeys(`#password`, "password123", chromedp.ByID),
		chromedp.Click(`button[type=submit]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#dash-runtimes-empty`, chromedp.ByID),
	)
	if err != nil {
		t.Fatalf("login flow: %v", err)
	}

	// The dashboard's own "Help: Dashboard" icon opens a real
	// bootstrap.Popover with content fetched from help/dashboard.md.
	var popoverText string
	err = chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-help="dashboard"]`, chromedp.ByQuery),
		chromedp.Click(`[data-help="dashboard"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.popover .popover-body`, chromedp.ByQuery),
		chromedp.Text(`.popover .popover-body`, &popoverText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("dashboard help popover: %v", err)
	}
	if !strings.Contains(popoverText, "live glance") {
		t.Fatalf("dashboard popover body = %q, want the dashboard.md summary", popoverText)
	}

	// The full manual page renders Markdown client-side and lists every
	// topic in help/index.json down the TOC.
	var manualHeading, tocHTML string
	err = chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/help.html"),
		chromedp.WaitVisible(`#help-content h1`, chromedp.ByQuery),
		chromedp.Text(`#help-content h1`, &manualHeading, chromedp.ByQuery),
		chromedp.OuterHTML(`#help-toc`, &tocHTML, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("help manual page: %v", err)
	}
	if manualHeading != "Dashboard" {
		t.Fatalf("help.html default topic heading = %q, want Dashboard", manualHeading)
	}
	if !strings.Contains(tocHTML, "Terminal") || !strings.Contains(tocHTML, "Databases") {
		t.Fatalf("help.html TOC missing expected topics: %s", tocHTML)
	}

	// The search box filters the TOC to topics whose content matches — jQuery's
	// .toggle() sets display:none rather than removing the element, so check
	// each row's own style attribute rather than the TOC's outer HTML (every
	// row's text stays present in the DOM either way).
	var dashboardStyle, servicesStyle string
	err = chromedp.Run(ctx,
		chromedp.SendKeys(`#help-search`, "systemd", chromedp.ByID),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.AttributeValue(`[data-toc="dashboard"]`, "style", &dashboardStyle, nil, chromedp.ByQuery),
		chromedp.AttributeValue(`[data-toc="services"]`, "style", &servicesStyle, nil, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("help search: %v", err)
	}
	if !strings.Contains(dashboardStyle, "display: none") {
		t.Fatalf("help search for \"systemd\" should hide the unrelated Dashboard topic, style = %q", dashboardStyle)
	}
	if strings.Contains(servicesStyle, "display: none") {
		t.Fatalf("help search for \"systemd\" should keep the Services topic visible, style = %q", servicesStyle)
	}
}
