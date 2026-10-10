# REQUIREMENTS_ANDROID_PARITY.md — Android client feature-parity, round 1

Status: requirements only (discovery complete 2026-10-10). No design
decisions belong in this file — architecture follows in design/ADR docs.

## Context

The Android client (ADR-053) is terminal-complete and on-device verified.
The web workspace carries nine app tabs; Android serves one (Terminal) plus
a project-list Overview. Parity round 1 (user decision, this doc): add
**Journal** and **Editor** to Android. All other web tabs (Commands, Files
manager, Source Control, Databases, Backups, Docker) stay web-only and are
candidates for later rounds.

Existing Android inventory (verified in code):

- Screens: server list, Pair, Projects, Terminal (tabs/dock/macros), Settings.
- API in use: device pair, `GET /projects`, `GET /runtimes`, session CRUD +
  attach/detach/resize/signal, per-session WebSocket stream.
- Consume-nothing surfaces today: everything under `/files/`, `/editor/`,
  `/journal/*`, and the web-only app pages (login/2FA, users, audit,
  runtimes management) — the app-level admin pages stay web-only by design.

## Goal

On a paired Android device, a user can do the two things the phone is
actually for when away from the desktop: read and write the project
Journal, and open, edit, and save project files and journal notes.

## Functional requirements

### FR-1 — Journal tab (per project)

1. List a project's journal notes: title, updated time, attachment count.
   Same visibility rules as the web (server-authorizes; project membership
   decides access).
2. Read a note: rendered Markdown body plus its attachments.
3. Create, edit, and save notes' title and body through the existing
   journal REST surface.
4. Delete own (and otherwise-permitted) notes via the same API.
5. Attachments: attach camera photos and local files to a note; download
   (open with share/intent) existing attachments; remove attachments they
   can remove.
6. Note bodies render as rich text (resolved design finding: the journal
   API stores `body_html`, Summernote HTML sanitized at render time —
   not Markdown; see docs/DESIGN_ANDROID_PARITY.md §1).
7. Notes edit over the REST API; no realtime co-editing protocol on
   Android in round 1 (OQ-4 covers the collision guarantee).

### FR-2 — Editor tab (server files)

1. Browse the project tree: directory listing + filename search, reusing
   the files REST surface read-only. No move/rename/mode/owner/upload in
   round 1 (web owns file management).
2. Open a file into an embedded syntax-highlighting code editor; save back
   through `files/content`.
3. Editing works on reasonable mobile-typical sizes: buffer up to ~1 MB
   without jank beyond that; graceful refusal (with message) on huge or
   binary files — mirror the web tab's own size/binary guards.
4. Save is conflict-safe: no silent overwrite of a newer server version
   (see OQ-4).

### FR-3 — one editing surface per document type (revised by design)

Journal notes edit in the rich-text surface (the API contract is HTML);
repo files edit in the code editor. No raw-Markdown mode for notes in
round 1 — that would need a server + web format migration (out of scope):
docs/DESIGN_ANDROID_PARITY.md §1.

## Non-functional requirements

1. **Auth**: everything rides the existing device-key Sig auth over HTTPS —
   no new credentials in the app.
2. **Security**: attachments download to app-private storage only; no
   arbitrary-path trust; nothing entered in the editor is logged. Existing
   FLAG_SECURE/screen policy applies to new screens.
3. **Resilience**: unsaved editor buffer survives process death (pending
   data is written before losing a session — same bar as muxalot's input
   handling). Journal drafts persist locally until saved.
4. **Performance**: 60 fps scroll in the editor; list screens lazy-load
   like the current terminal tabs.
5. **Compatibility**: minSdk 26 / current stack unchanged; new code lives
   in the existing `android/` module layout (`net/Api.kt`, `ui/`).
6. **No server changes required** in round 1: `journal` endpoints and
   `files` read/content endpoints exist in `openapi.yaml` today. If a
   design pass discovers a server gap, it is a documented deviation, not a
   silent one.

## User stories / acceptance

- **US-1**: On a Pixel, open a project's Journal tab, read a note, tap
  Edit, fix a typo in raw Markdown, save; the change is visible in the web
  UI. Accept: note round-trips between the two clients.
- **US-2**: Take a photo, attach it to a journal note on the phone, open
  the same note in the web UI and download the attachment. Accept: bytes
  round-trip.
- **US-3**: Browse to `src/main.rs` in the project tree, open it, add a
  line, save; the web UI's Code/Files tab shows the new content and the
  mtime advanced. Accept: save is observable server-side.
- **US-4**: With the same file open in the web editor, edit it on the
  phone and hit save after a web-side change landed. Accept: the app
  surfaces a conflict prompt instead of silently clobbering (OQ-4).
- **US-5**: Open a 900 KB text file, scroll and type with no visible
  stutter; open a 40 MB binary, receive the refusal message. Accept: both
  behaviors in one test path.

## Open questions (for the design pass, resolved there — not silently)

- **OQ-1 — Editor engine**: **resolved** (user directive + verification
  2026-10-10): sora-editor (`io.github.rosemoe:editor` 0.24.6,
  `language-textmate`) as an unmodified dependency; license
  **LGPL-2.1-or-later** (verified — not Apache), minSdk 21. Markor's GPL
  source stays out.
- **OQ-2 — Markdown renderer**: **dissolved** — the journal API carries
  `body_html`, not Markdown; notes render/edit in a WebView + DOMPurify
  (docs/DESIGN_ANDROID_PARITY.md §1/§5).
- **OQ-3 — Save collision**: **resolved** in design (§4): stat-mtime
  baseline + conflict dialog for files; notes are last-write-wins, same
  as the web. No server change.
- **OQ-4 — Attachment capture scope**: camera + pick-file is the floor;
  audio/other sources are not in scope unless zero new surface.
- **OQ-5 — Notification parity** (bell, session disconnect): out of round-1
  scope; listed here so it is remembered for a later round.
- **OQ-6 — Later-round candidates**: Commands (run + output view) then
  Docker logs were ranked most phone-natural after round 1; not committed.

## Explicitly out of scope (round 1)

Files manager operations beyond read/browse/open, Source Control, Database
browser, Backups, Docker, Commands tab, admin pages (users/audit/runtimes
management), password-based login in the app, realtime co-editing.