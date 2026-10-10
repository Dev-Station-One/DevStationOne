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
  green; the photo→web round trip is the stage-7 on-device E2E (Pixel runs
  the release-signed APK; debug switch needs uninstall + re-pair,
  deliberately not done in this slice — same as stages 1–2).
- Stages 4 (file tree), 5 (code editor), 7 (docs/roundoff) remain.