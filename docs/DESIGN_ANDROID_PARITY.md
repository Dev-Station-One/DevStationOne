# DESIGN_ANDROID_PARITY.md — Android parity round 1: Journal + Editor

Implements FR-1..FR-3 from `docs/REQUIREMENTS_ANDROID_PARITY.md`. No Control
Server changes; everything rides the openapi.yaml journal + files surfaces
with the existing device-key Sig auth (ADR-053).

## 1. Verified dependency decisions (OQ-1/OQ-2 resolved 2026-10-10)

- **sora-editor** — `io.github.rosemoe:editor:0.24.6` (BOM
  `io.github.rosemoe:editor-bom:0.24.6`), + `io.github.rosemoe:language-textmate:0.24.6`.
  License **LGPL-2.1-or-later** (verified on repo + Maven Central; my
  earlier Apache assumption was wrong). minSdk 21 (fine at 26; only the
  LSP module needs 26+). JDK 17 — matches our build. Requires **core
  library desugaring** on API < 33 (minSdk 26 ⇒ enable it):
  `isCoreLibraryDesugaringEnabled = true` +
  `coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.5")`.
  R8: module ships consumer keep rules; re-verify release build at slice 5.
  LGPL distribution note: keep the library as an unmodified Maven
  dependency (standard AAR relink path), add notices in
  `android/app/src/main/assets/VENDOR.md`, no source vendoring.
- **Markdown/HTML rendering** — none needed: journal bodies arrive as
  `body_html` (Summernote HTML, sanitized by the client at render time).
  Requirements' "markdown renderer" question dissolved; notes are HTML end
  to end, same as the web. Repo files are edited as plain text in
  sora-editor; no markdown conversion anywhere.
- **No Markow/Markwon/Markor/Squircle-CE code is copied.** Markor is GPLv3
  (contamination), Squircle-CE itself only contributed the pointer to its
  engine (sora-editor), which we now depend on directly.

### Requirement corrections this design forces (requirements doc updated in step)

- FR-1.6: notes are HTML, not Markdown. Rendering = Android WebView +
  DOMPurify (same sanitizer the web uses), edit = contenteditable.
- FR-3 as written (note as raw Markdown in the code editor) is replaced by
  FR-3': one rich-text editing surface for notes; the code editor is for
  repo files only. Changing the note format to a text/markdown flow would
  require a server + web migration — explicitly out of round 1

## 2. Component map (files to create/modify — all in the existing `android/` module)

```text
android/app/src/main/java/dev/devstationone/
├── MainActivity.kt            + Screen.Workspace (was Screen.Term)
├── net/Api.kt                 + journal models/JournalPage + journal/note/deleteNote
│                              + (stage 2/3: putNote, upload/download attachment)
│                              + (stage 4/5: listFiles/searchFiles/statFile/readFile/writeFile)
├── ui/Workspace.kt            (top TabRow workspace; hiddenOther measure-but-not-place pattern)
├── ui/TerminalScreen.kt       TerminalBodyContent (the Terminal tab; header lives in workspace)
├── ui/JournalScreen.kt        (Journal tab list + note overlay NoteScreen: view, menu/delete)
├── ui/HtmlPanel.kt            (shared WebView: DOMPurify render, contenteditable
│                               edit mode, request interception bridge — stage 2+)
├── ui/FileTreeScreen.kt       (stage 4: list + drill-in + name search)
└── ui/CodeScreen.kt           (stage 5: sora-editor CodeEditor in AndroidView, save bar)

android/app/src/main/assets/
├── note.html                  (journal view+edit page: DOMPurify-sanitized setBody)
├── vendor/dompurify/*         (vendored from the web client's same file)
├── tm-grammars/…              (stage 5: TextMate grammar subset + one dark theme JSON)
└── VENDOR.md                  + license notices (LGPL sora-editor, DOMPurify, grammars)
```

Modification only of existing files: `net/Api.kt` (methods follow the
existing `request()`/`check()` pattern), `MainActivity.kt` (Screen entries
+ navigation branches, matching `Terminal`'s shape),
`android/app/build.gradle.kts` (deps + desugaring), `assets/VENDOR.md`,
`docs/*`.

## 3. Screens and navigation

Current stack: `List → Pair / Projects → Term`, plus `Shortcuts`, `Settings`
(`Screen` sealed interface, `MainActivity.kt:34`). Round 1 reshapes the
project level into a **workspace screen with a top tab bar** (user
decision, 2026-10-10):

```text
Projects card (existing) ──open──► ProjectWorkspace (new)
      PrimaryTabRow, round 1:  [ Terminal | Journal | Files ]
      later rounds append:       Overview · Commands · Docker · Git …
      (tab row is horizontal-scrollable when round-2 tabs arrive)

      Terminal tab  = today's TerminalScreen body (chips/LazyRow + panes +
                      dock) unchanged inside the tab
      Journal tab   = JournalScreen list ──tap note──► NoteScreen (full
                      screen, no tab bar, BackHandler returns)
      Files tab     = FileTreeScreen ──tap file──► CodeScreen (full screen)
```

- **Tab switching never unmounts a tab's content.** Terminal panes keep
  their WebViews and live streams; workspace is a `Box` holding all three
  tab containers, toggled with the existing `setActive(visible)` /
  visibility-GONE pattern (same mechanism session chips already use). No
  reattach, no lost terminal state when flipping to Journal mid command.
- One tab row instance, round-1 tabs enabled; round-2 tabs appear disabled
  with a "round 2" label only when their stage lands (no dead tabs today).
- Deep screens (NoteScreen, CodeScreen) hide the tab bar: focus-first
  mobile editing, back navigation restores the workspace with state.
- New `Screen` entries: `Workspace(server, projectID, projectName)` and
  `Note`, `FileTree`, `Code` sub-stacks; the old `Screen.Term` becomes the
  Terminal tab's content component, unchanged internally.
- Per-tab state: Journal list scroll position and Files path survive tab
  switches (remembered in the workspace), not process death (durable
  drafts are covered in §6).

Surface anatomy (detail kept from the original §3):

- **JournalScreen**: paged list (`GET /journal?limit&offset`), newest
  first; snippet + `updated_at` + attachment count per row; FAB creates
  (`POST /journal`); long-press = delete confirm.
- **NoteScreen view**: native attachment chips (icon, filename, size; tap
  = download → open/share; long-press = save), rendered body below,
  attachments of the note listed too (metadata row). Edit toggles edit
  mode in place.
- **NoteScreen edit**: `HtmlPanel` edit mode — contenteditable WebView +
  native toolbar (B/I/U · H2/H3 · lists · quote · code · link · clear)
  via `document.execCommand`. Save = `PUT /journal/notes/{id}`
  (`body_html`); new-note = `POST` then rebind id. Commit metadata is
  display-only on the round-1 Android client.
- **FileTreeScreen**: breadcrumbs + `GET /files?path=…` drill-in, dirs
  first; search field → `GET /files/search?q=&path=`, result tap opens
  CodeScreen.
- **CodeScreen**: sora-editor `CodeEditor` in `AndroidView`; top bar =
  filename, dirty dot, save, reload; `release()` on dispose; text
  restored from the §6 draft cache if a dirty draft exists.

## 4. Save/collision policy (OQ-3 resolved — no server change)

`files/content` is raw GET/PUT with no ETag; `FileInfo` carries `mtime`.
Policy: pure-logic, unit-tested:

```text
open:   stat → record (mtime, size);  GET content → buffer
save:   stat now ≠ recorded AND content-unchanged-since-open?  → RELOAD prompt
        stat now ≠ recorded AND buffer user-edited            → CONFLICT dialog:
                                                                 keep mine (PUT anyway)
                                                                 reload mine (re-GET)
        stat unchanged → PUT → re-stat → new baseline recorded
```

Small race window (stat↔PUT) remains: accepted and documented — identical
to the web tab's current behavior; an If-Match hardening would be a server
change (noted for a later round only).

Editor guards: refuse > 4 MB in the editor (read-only notice with an
"open on web" hint), warn > 1 MB soft, reject any NUL-byte-sniffed binary
file. 24 MB API cap never reached from the editor.

Journal notes: **last-write-wins**, same as the web's two-browser case —
`updated_at` shown in the editor footer ("last updated …") so the
behavior is observable, not silent. Concurrency guard on notes is out of
round 1 (no conditional PUT exists to build on).

## 5. WebView rendering & image/attachment loading (the auth problem)

The browser loads note `body_html`'s inline `<img>` tags (which point at
`/api/v1/.../attachments/{id}` and files URLs) with cookie auth. The app
has Sig headers on every request instead. `HtmlPanel` therefore installs a
`WebViewClient.shouldInterceptRequest` rule: subresource URLs matching
`/api/v1/.../attachments/` (and `/files/content`) are fetched by OkHttp
with the Sig header and piped into the WebView as synthetic responses.
View-mode output is sanitized with the vendored DOMPurify inside the
`note.html` asset page (never raw). Edit-mode output is also sanitized on
`onSave` before it leaves the app — the HTML the app sends is the same
shape the browser would produce.

## 6. Draft persistence (NFR-3)

- Notes: `ShortcutStore`-style SharedPreferences JSON
  (`drafts-{projectID}` → noteID/{new}: title/body_html), kept until the
  server save succeeds, then removed.
- Files: dirty buffer written to `cacheDir/editor-store/{server}/{project}/`
  (path-hashed filename) on app-pause and on back-navigation of a dirty
  screen; cleared on successful save. Never in SharedPreferences (size).

## 7. Dependencies + build deltas (all verified current)

```kotlin
implementation(platform("io.github.rosemoe:editor-bom:0.24.6"))
implementation("io.github.rosemoe:editor")                     // 0.24.6
implementation("io.github.rosemoe:language-textmate")          // 0.24.6
coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.5")
compileOptions { isCoreLibraryDesugaringEnabled = true }
```

TextMate assets: a language subset (kotlin/java/json/yaml/go/ts/js/css/html/
markdown/shell/dockerfile/toml/ini/c/cpp/csharp/php/rust/sql) + one dark
theme, vendored with per-file license notes in `assets/VENDOR.md`
(same vendoring policy as the existing xterm/fonts, per
`docs/REQUIREMENTS_ICON_SYSTEM.md`). Language resolution is a
extension→grammar map, unit-tested.

APK growth estimate: +2–4 MB (grammars + sora-editor), from 11.5 MB.

## 8. Slices (stages; each ships vertical: code+tests+docs+CodeGraph+commit)

1. **Journal list + note view** — JournalScreen, HtmlPanel view mode,
   `GET /journal`, note GET/DELETE, drafts ignored here. Accept: US-1 read
   half; unit: paging/error paths.
2. **Note edit + save** — edit mode + toolbar + PUT + drafts store;
   sanitize-on-save enforced. Accept: US-1 full round trip.
3. **Attachments** — upload (file + camera), download/share, chips,
   25 MB/10-per-note client limits respected, request interception for
   inline images. Accept: US-2.
4. **FileTree** — list + search + guards. Accept: navigation works on
   device, empty/huge dirs lazy-load.
5. **Code editor** (the dependency slice) — sora-editor + textmate assets +
   desugaring; read-only first, then save/reload flow + collision logic
   (unit-tested). Accept: US-3, US-5.
6. **Note-in-file editor bridge** — none in round 1 after FR-3' (§2);
   slice removed.
7. **Docs + parity roundoff** — ANDROID_CLIENT.md scope update,
   REQUIREMENTS mark round-1 status, IMPLEMENTATION_PLAN statuses,
   CodeGraph, `assembleDebug` + `assembleRelease` green, on-device E2E
   (Pixel 8a, dev-server pairing) checklist like Slice 25's.

Server-side/contract tests: none required (no change); each new endpoint
call is exercised by the on-device E2E and by the web UI's existing
integration tests.

## 9. Risks

| Risk | Mitigation |
|---|---|
| LGPL-2.1 in distributed APK | Unmodified Maven AAR dependency + notice; no source fork. Documented in VENDOR.md |
| `execCommand` deprecation | Works in WebView today; scope is a fixed minimal toolbar; revisit only if Chrome removes it |
| TextMate grammar weight / load jank | Subset + lazy grammar load on file open; measured in on-device E2E |
| contenteditable + IME quirks (Chinese/Japanese…) | Round-1 note editing is plain-text-shaped HTML; watch E2E; terminal IME path unchanged |
| sora-editor release-mode R8 issues | Keep rules already shipped by the lib; re-run `assembleRelease` in slice 5 before merge |
| Attachment URLs inside body_html | All go through the intercept rule (§5); no unsanitized render |

## 10. Status

### Deviation (documented, 2026-10-10)

The requirements' "no server changes" assumption broke on first on-device
use: the journal routes were `requireSession` (cookie-only), so every
device-signed request 401'd (exactly `docs/REQUIREMENTS_ANDROID_PARITY.md`
NFR-6's predicted case). Fixed per the requirements' escape hatch —
ADR-053 addendum: all 8 journal routes use `requireIdentity` (Sig header
or cookie fallback, web unchanged), `deviceSig` security entries added in
openapi.yaml (tag Journal), `TestDeviceJournalAuth` proves owner device
access + stranger isolation + anonymous 401. Redeployed to the dev
instance and to prod (`dev.brian.one`, ds.premadev.com). Later parity
stages hitting cookie-only routes (the Files/code-editor routes for
stages 4–5) will need the same one-line route switch in their slice.

Round 1 implementation:
- **Stage 1 (journal list + note view, workspace tab bar) — complete** (2026-10-10):
  `Screen.Term` became `Screen.Workspace` (`ui/Workspace.kt`) with a
  material3 `TabRow` (Terminal/Journal); `TerminalScreen`'s body is now
  `TerminalBodyContent` (own action row, header actions moved with it),
  always composed and zero-size-hidden while the Journal tab shows, so
  hidden tabs keep their WebViews, PTY streams and list state. Notes open
  as a full-screen overlay (`JournalScreen.kt`'s `NoteScreen`) — the
  workspace never unmounts. Rendering is `ui/HtmlPanel.kt` + the new
  `assets/note.html` (DOMPurify 3.1.6 vendored from the web client's file,
  VENDOR.md updated); server-URL subresource loads ride `shouldInterceptRequest`
  → OkHttp Sig header. `net/Api.kt` gained journal models + `journal`,
  `note`, `deleteNote`. `assembleDebug` + `testDebugUnitTest` green
  (`JournalModelsTest` decodes openapi-shaped payloads).
- **Stage 2 (note edit + save) — complete** (2026-10-10): `HtmlPanel` edit
  mode (contenteditable + debounced sanitized-push bridge), native
  execCommand toolbar (B/I/U · H2/H3 · lists · quote · code · link ·
  clear) in `NoteScreen`, `Api.putNote` (PUT, `bodyOf` JSON), and
  `data/NoteDraftStore` (per-project SharedPreferences draft, deleted only
  after a successful PUT — resume-after-crash per NFR-3). Save flushes the
  debounced push (150 ms) before reading the draft. Sanitize-on-save
  enforced: every byte typed stays inside the WebView DOM until the page
  pushes DOMPurify output, and that output is what PUT sends. `note.html`
  also gained proper base64-UTF-8 (plain `atob` would mojibake non-ASCII
  bodies — caught while wiring edit round trip). Accept US-1: build +
  `JournalModelsTest`/`NoteEditArgsTest` green; the on-device round trip
  is the stage-7 E2E — the attached Pixel runs the release-signed APK and
  switching to debug needs uninstall + device re-pair, deliberately not
  done in this slice.
- **Stage 3 (attachments) — complete** (2026-10-10): `Api.uploadAttachment`
  (multipart `file` POST) and `Api.downloadAttachment` (stream-to-stream GET);
  chips are now tappable — tap = download to `cacheDir/share/` + share sheet
  (`ACTION_SEND`, FileProvider `dev.devstationone.fileprovider`), long-press =
  save into MediaStore's Downloads (API 29+; earlier versions get a
  "share it instead" message, `ponytail:` no WRITE_EXTERNAL_STORAGE path).
  Upload sources are `OpenDocument` (any type, display name from
  OpenableColumns) and `TakePicture` (fixed `cacheDir/camera/capture.jpg`
  FileProvider output, `res/xml/file_paths.xml` new, provider added to the
  manifest). Client limits enforced pre-upload: 25 MB and 10 per note
  (mirror the server's checks; the server stays authoritative). Inline
  images in `body_html` already render — `shouldInterceptRequest` bridges
  every `/api/v1/` GET with the Sig header, unchanged in this stage. What
  is deliberately not done: attaching from the phone does not insert the
  image into the note body (the web editor does that); the attachment is
  metadata + a chip, which is what US-2's bytes round trip needs — add
  insert-into-body if the flow demands it. Accept US-2: build +
  `AttachmentNamesTest` (server-filename sanitization for cache paths)
  green; on-device E2E on the Pixel 8a (release-signed APK, incl. the
  FAB follow-up) confirmed working by the user 2026-10-10 — no debug
  switch / device re-pair needed, so the stage-7 E2E does not re-run
  this.

  Follow-up found during the on-device pass (same day): the Journal tab
  had no way to create a note — added the `New note` FAB to
  `JournalScreen`, `Api.createNote` (POST /journal with a blank-paragraph
  seed body, since the server rejects an empty `body_html`) opening
  `NoteScreen` straight into edit mode via a `startEdit` flag threaded
  through Workspace. The created note is prepended to the list at once;
  flagging: notes deleted from `NoteScreen` still linger in the list
  until the tab is reloaded — wire a list refresh when that bites.
- **Stage 5 (code editor) — complete** (2026-10-10, pending on-device E2E):
  `ui/CodeScreen.kt` hosts a sora-editor `CodeEditor` (0.24.6 via BOM +
  `language-textmate`) in `AndroidView`, released on dispose, wired to
  `Api.statFile`/`readFile`/`writeFile` (raw-byte GET/PUT). TextMate
  highlighting: 20 grammars vendored from npm `tm-grammars` 1.32.22 (MIT;
  LICENSE+NOTICE in `assets/textmate/grammars/`), one dark theme
  (`darcula.json` from sora-editor's own sample assets), and an in-repo
  `languages.json` without `languageConfiguration` entries (symbol-pair
  autocomplete off) — extension→scope map `grammarFor()` is unit-tested;
  files outside the map render plain. The registries are process-wide
  statics, set up once under a lock. The §4 flow is all in: stat baseline
  at open, >4 MB refusal notice, >1 MB soft-warn dialog, NUL-sniff
  binary refusal, `saveDecision()` collision table (unit-tested) driving
  the save button (free PUT / "reload" prompt for an untouched stale copy
  / keep-mine-or-reload conflict dialog), re-stat as the new baseline
  after save, and the §6 draft: dirty-buffer snapshots to
  `FileDraftStore` (`cacheDir/editor-store/{server}/{project}/`,
  path-hashed) on back-nav and ON_STOP, cleared on successful save,
  restored over the fetched copy on open. Dependency delta beyond §7:
  the project's Kotlin toolchain moved 2.0.21 → 2.3.10 (sora-editor 0.24.6
  ships 2.3-metadata modules; `kotlinOptions` DSL migrated as required) —
  a build-compat change, not a design change. Server: stat/content
  GET+PUT switched to `requireIdentity` (openapi `deviceSig` entries;
  `TestDeviceFilesAuth` covers the four editor-ish routes), deployed to
  dev + prod via `scripts/deploy-to-prod.sh`; the
  create/mkdir/rename/copy/move/mode/owner/delete writers stay
  cookie-only (round-1 Files is browse + edit-content only). APK
  5.4 MB release (+2.1 MB, inside the §7 estimate). Accept US-3/US-5:
  unit tests green, `assembleDebug`/`assembleRelease` green; on-device
  edit-and-save (mtime observable on web) and the 900 KB / 40 MB paths
  await the Pixel E2E.
- **Stage 7 (docs + roundoff) — complete** (2026-10-10): round 1 closed.
  Requirements doc marked shipped
  (`docs/REQUIREMENTS_ANDROID_PARITY.md` "Round-1 outcome" — acceptance
  status per US, including the one caveat: US-4's conflict dialog is
  implemented + unit-tested but was not separately staged on device),
  `docs/IMPLEMENTATION_PLAN.md` Slice 26 completed with the per-stage
  log and the on-device E2E record, `docs/ANDROID_CLIENT.md` scope
  paragraph finalized. Design doc §10 above is the per-stage history
  (stages 1–5 + the device-pass follow-ups). OQ-6 ranks the next
  round's candidates.

  On-device follow-ups the same day (user-confirmed working; commits
  48c816f and d60d728, plus the size/preview pass): the empty-editor race
  (file text landing before AndroidView's factory creates the CodeEditor
  is now parked and applied in the update block), a **word-wrap
  Settings switch** (default on), a **code editor font size**
  (Settings, 12/14/16/18/20 sp presets, default 14), and a **rendered
  View/Edit toggle** for markdown and HTML files in the editor top bar:
  preview floats over (never replaces) the editor so edit state,
  undo history and scroll survive the round trip; markdown renders via
  the web client's own `marked` 12.0.2 (vendored, MIT) and everything
  sanitizes through the note view's DOMPurify; HTML files render
  scripts-stripped through DOMPurify whole-document mode; relative
  image/stylesheet refs are rewritten to `files/content` URLs served by
  the now-shared Sig-header bridge (`bridgeServerResponse`, extracted
  from HtmlPanel, used by note views and the preview). Preview payload
  rides the load URL b64-url-encoded (`assets/preview.html`,
  `vendor/marked/` new in VENDOR.md). `previewable`/`previewIsHtml`
  unit-tested; `CodeScreenLogicTest` extended.

- **Stage 4 (file tree) — complete** (2026-10-10): third workspace tab
  "Files" hosts a new `ui/FileTreeScreen.kt` — breadcrumb drill-in over
  `GET /files?path=…` (root ".", dirs-first sort identical to the web
  tree), a name-substring search box riding `GET /files/search?q=…`
  scoped to the current directory, empty/huge dirs rendered lazily in a
  `LazyColumn`, and tab-hidden content kept composed via the same
  `hiddenOther` measure-but-not-place pattern (list state survives tab
  switches). `net/Api.kt` gained `FileInfo`, `listFiles`, `searchFiles`;
  `fmtSize` is shared with the journal now (`internal`). File rows are
  tappable only for directories — opening a file is the stage-5
  editor's surface, so file and search-result rows are inert until then
  (deliberate scope boundary, not a stub). Server side, the ADR-053
  addendum route switch landed for the two routes this stage calls
  (`GET /files`, `GET /files/search` → `requireIdentity`,
  `deviceSig` security entries in openapi.yaml, web unchanged); the
  editor routes switch in stage 5. Two test-infrastructure bugs were
  fixed while validating — both only visible with
  `DEVSTATIONONE_TEST_DATABASE_URL` actually set (integration tests
  silently skip without it, which is how the journal stage's device
  test passed): `signedRequest` sent absolute-form targets so
  `r.RequestURI` carried the full URL while the test signed only the
  path (origin-form is what the wire and the Android client use — the
  helper now sends origin-form); and the stranger-isolation assertions
  assumed per-user project scoping that does not exist — every
  authenticated user sees every project by design
  (internal/api/projects.go listProjects comment), so stranger-device
  expectations now assert the shared-visibility model (200/204/409)
  instead of 404. `TestDeviceFilesAuth` added on the same pattern.
  Accept (US navigation half): unit `FileTreePathsTest` (crumbs/subPath/
  dirsFirst/glyphs), `make test` green with the test DB actually
  attached, `assembleDebug`/`assembleRelease` green; on-device
  drill-in/search E2E on the Pixel 8a confirmed working by the user
  2026-10-10 — required deploying the route switch to prod
  (`dev.brian.one` via `scripts/deploy-to-prod.sh`): the phone pairs to
  ds.premadev.com, so the dev-instance-only deploy left its Files tab
  401-ing "not authenticated" until prod shipped the same binary.