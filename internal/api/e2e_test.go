package api

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pquerna/otp/totp"

	"devstationone/internal/auth"
	"devstationone/internal/projects"
	"devstationone/internal/runtimes"
)

// e2eLoginWithEnrollment drives a real browser through login.html's
// password form and mandatory-2FA enrollment (docs/DECISIONS.md ADR-047)
// via the TOTP path, ending on the dashboard. Every account starts
// unenrolled, so every E2E test logging in needs this instead of a bare
// form submit.
func e2eLoginWithEnrollment(t *testing.T, ctx context.Context, serverURL, email, password string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(serverURL+"/login.html"),
		chromedp.WaitVisible(`#email`, chromedp.ByID),
		chromedp.SendKeys(`#email`, email, chromedp.ByID),
		chromedp.SendKeys(`#password`, password, chromedp.ByID),
		chromedp.Click(`button[type=submit]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#enroll-panel`, chromedp.ByID),
		chromedp.Click(`#enroll-choice-totp`, chromedp.ByID),
		chromedp.WaitVisible(`#enroll-totp-uri`, chromedp.ByID),
	); err != nil {
		t.Fatalf("login + start totp enrollment: %v", err)
	}

	var otpauthURL string
	if err := chromedp.Run(ctx, chromedp.Text(`#enroll-totp-uri`, &otpauthURL, chromedp.ByID)); err != nil {
		t.Fatalf("read otpauth url: %v", err)
	}
	parsed, err := url.Parse(otpauthURL)
	if err != nil {
		t.Fatalf("parse otpauth url: %v", err)
	}
	code, err := totp.GenerateCode(parsed.Query().Get("secret"), time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.SendKeys(`#enroll-totp-code`, code, chromedp.ByID),
		chromedp.Click(`#enroll-totp-confirm`, chromedp.ByID),
		chromedp.WaitVisible(`#backup-codes-panel`, chromedp.ByID),
		chromedp.Click(`#backup-codes-continue`, chromedp.ByID),
		chromedp.WaitVisible(`#dash-runtimes-empty`, chromedp.ByID),
	); err != nil {
		t.Fatalf("confirm totp enrollment: %v", err)
	}
}

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
	router, _, _, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey, "", "", TwoFactorConfig{})
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
	e2eLoginWithEnrollment(t, ctx, server.URL, "e2e-admin@example.com", "password123")

	var pageTitle string
	err := chromedp.Run(ctx, chromedp.Title(&pageTitle))
	if err != nil {
		t.Fatalf("read page title: %v", err)
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
	router, _, _, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey, "", "", TwoFactorConfig{})
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

	e2eLoginWithEnrollment(t, ctx, server.URL, "e2e-help@example.com", "password123")

	// The dashboard's own "Help: Dashboard" icon opens a real
	// bootstrap.Popover with content fetched from help/dashboard.md.
	var popoverText string
	err := chromedp.Run(ctx,
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

// TestE2E_AboutModal drives a real headless browser through the sidebar's
// "About" link (web/static/js/app.js's renderAboutModal/#about-btn),
// proving the modal opens via tabler.Modal and closes cleanly — this is
// exactly the kind of thing a dual-Bootstrap page (project.html loads the
// real bootstrap.bundle.min.js for Tab, alongside tabler.min.js) can
// silently break, per the notifications-bell dropdown collision recorded
// in ADR-034, so the assertion runs against project.html specifically.
func TestE2E_AboutModal(t *testing.T) {
	chromiumPath := findChromium(t)
	db := testDB(t)
	ctx0 := context.Background()

	user, err := auth.CreateUser(ctx0, db.Pool, "e2e-about@example.com", "password123", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	router, _, _, _, _ := NewRouter(db, logger, "../../web/static", testDBEncryptionKey, "", "", TwoFactorConfig{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	rt, _, err := runtimes.Create(ctx0, db.Pool, "e2e-about-runtime", user.ID)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	proj, err := projects.Create(ctx0, db.Pool, projects.Fields{
		Name:             "e2e-about-project",
		RuntimeID:        rt.ID,
		WorkingDirectory: "/tmp",
		Shell:            "/bin/bash",
	}, user.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

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

	e2eLoginWithEnrollment(t, ctx, server.URL, "e2e-about@example.com", "password123")

	err = chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/project.html?id="+proj.ID),
		chromedp.WaitVisible(`#app-help-btn`, chromedp.ByQuery),
		// #about-btn lives inside #sidebar-menu, which the headless
		// browser's default (narrow) viewport collapses behind the mobile
		// navbar toggler — same reason TestE2E_LoginAndActivityPage clicks
		// this before touching any other sidebar content. The expand is a
		// CSS-transitioned Bootstrap Collapse, not instant: WaitVisible
		// alone passes mid-transition (box model already computable), so a
		// click right after lands while #about-btn is still moving unless
		// given time to settle into a stable .show first.
		chromedp.Click(`button[aria-label="Toggle navigation"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#about-btn`, chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("login + open project: %v", err)
	}

	var modalHeading string
	err = chromedp.Run(ctx,
		chromedp.Click(`#about-btn`, chromedp.ByID),
		chromedp.WaitVisible(`#about-modal.show`, chromedp.ByQuery),
		chromedp.Text(`#about-modal h3`, &modalHeading, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("open about modal: %v", err)
	}
	if modalHeading != "DevStationOne" {
		t.Fatalf("about modal heading = %q, want DevStationOne", modalHeading)
	}

	// Same settle reasoning as the sidebar expand above: WaitVisible passed
	// as soon as .show was added, but Bootstrap's modal fade-in transition
	// is still moving the dialog into place at that instant. And unlike the
	// open check, wait on the stable #about-modal id rather than a
	// compound `.show` selector: Bootstrap's hide() keeps the same element
	// in the DOM and just removes .show/display — it never stops matching
	// a plain `#about-modal.show` selector by disappearing, so waiting on
	// the id is both correct and the more standard WaitNotVisible usage.
	err = chromedp.Run(ctx,
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Click(`#about-modal-close`, chromedp.ByID),
		chromedp.WaitNotVisible(`#about-modal`, chromedp.ByID),
	)
	if err != nil {
		t.Fatalf("close about modal: %v", err)
	}
}
