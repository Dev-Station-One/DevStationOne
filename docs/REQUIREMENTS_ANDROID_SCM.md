# REQUIREMENTS_ANDROID_SCM.md — Android Source Control tab (parity round 2)

Status: requirements discovery complete (2026-10-10), produced by the
`/sc:brainstorm` pass. Architecture/design follows in a design doc +
ADR if needed — nothing here decides implementation.

Reference UX: VSCode/VSCodium's Source Control view, as demonstrated by
microsoft/vscode-extension-samples `source-control-sample` — a changes
pane grouped by staging state, per-file diff links, a commit box,
branch indicator with sync state, and refresh. Phone translation kept
minimal on purpose.

## Context

- DevStation's git backend already covers nearly the whole VSCode SCM
  data model: the runtime runs real `git` (ADR-028, argv-only, audit
  events) and the Control Server exposes status/stage/unstage/commit/
  pull/push/diff/log (ADR for the routes; consumed by the web's Source
  Control surface in `web/static/project.html`). Android round 1
  deliberately left Source Control web-only (REQUIREMENTS_ANDROID_PARITY).
- The user's priority has changed: Source Control on the phone outranks
  the other round-2 candidates (OQ-6 ranked Commands first — superseded
  by this doc).
- Android round 1 precedent applies: every HTTP route the app calls gets
  `requireIdentity` + openapi `deviceSig` security entries, one route
  switch per route, web cookie path unchanged (stages 4-5;
  `TestDeviceFilesAuth` is the test pattern).
- The phone pairs to prod (ds.premadev.com): a route switch is only
  "ready" for E2E once the prod binary carries it
  (`scripts/deploy-to-prod.sh` — established flow).

## Goal

On a paired Android device, a user can review what changed in the
project's working tree, stage and commit it, sync with the remote, and
recover — the whole desktop loop, sized for a phone.

## Functional requirements

### FR-A — Git tab (per project, workspace tab bar)

A fourth workspace tab "Source Control" ("SCM" label space pending
design), alongside Terminal / Journal / Files; hidden-but-composed
preservation per the round-1 `hiddenOther` pattern.

0. **Tab bar converts to icons** (user direction, 2026-10-10 — supersedes
   design doc §3's horizontal-scrollable text row): with four tabs and
   the round-2+ roadmap, the workspace tab bar switches to icon-only
   tabs (VSCodium activity-bar model) — 24 dp stroke icons drawn in
   `MuxIcons`'s own style (no icon library enters the app for this),
   each carrying an accessibility/contentDescription label. New icons
   needed at minimum: Terminal, Journal, Branch, and Folder is reusable
   for Files; Commands/Docker icons land with those tabs in a later
   round. Text labels in the selected tab (Material's
   selected-label pattern) is the fallback if icons-only proves unclear
   on device (OQ-9).

1. **Status view**: changed files grouped Staged Changes / Changes
   (unstaged) / (conflicts when present) — per-file index and worktree
   XY status codes from `GET /git/status`, rename display via
   `orig_path`. Branch header: name (or "detached HEAD"), upstream,
   ahead/behind counts, remote presence.
2. **Diff view**: tap a file → its unified diff; staged (`staged=1`) and
   unstaged both reachable. Diffs render read-only (round 2 does not
   promise inline editor decorations).
3. **Stage/unstage**: per-file from the list; "stage all"/"unstage all"
   shortcuts. Existing `POST /git/stage|unstage` per path.
4. **Commit**: commit box (multiline) committing the staged index via
   `POST /git/commit`; empty message rejected client-side (server
   already does). After commit: staged list clears via refresh.
5. **Pull/push**: pull reports merge conflicts in-band (200 + conflicts
   list, never as an error — existing semantics); push reports
   "published new branch" when the server set an upstream. Buttons
   reflect `HasRemote`/`Upstream` (no remote → disabled).
6. **History**: `GET /git/log` — recent commits (sha, subject, author,
   time), paged or bounded; read-only (no checkout/revert of history in
   round 2).
7. **Refresh**: explicit refresh + refresh on tab re-entry; no live
   file-watch subscription required on Android (the server's editor
   pushes exist but round 2 stays request/response).

### FR-D — Discard changes (user-selected stretch scope)

8. **Discard per file and discard all**: server+runtime work — new git
   action(s) (`git restore` / `git checkout --`) plus Control Server
   routes, protocol payload fields, audit events, and openapi entries.
   Confirmation dialog per action is REQUIRED (destructive, no undo);
   never touches files without one. Untracked files: "discard" deletes
   or leaves them? (open question OQ-3.)

### FR-B — Branch switch / amend (user-selected stretch scope)

9. **Branch list & checkout**: read branches (local, with checked-out
   marker; remotes display-only), checkout a local branch — requires
   new git actions + protocol fields + routes (status already reports
   the current branch). Checkout with a dirty tree must refuse or
   confirm (open question OQ-4).
10. **Amend**: amend the last commit with the current staged index and
    a (possibly edited) message — new git action + route. Never amends
    a pushed commit knowingly? (open question OQ-5 — simple rule
    candidate: refuse when upstream branch is strictly not-behind.)

### FR-C — Web parity (informational, not this round)

The web's own SCM surface already consumes everything existing; FR-D/
FR-B's new backend capability (discard/branch/amend) may be wired into
web later — not part of this round's deliverable, but the routes land
openapi-described so it can.

## Non-functional requirements

1. **Auth**: device Sig auth; per-route `requireIdentity` + `deviceSig`
   openapi entries; tests mirror `TestDeviceFilesAuth` (owner/stranger/
   anonymous per route group).
2. **Safety**: commit messages pass stdin (already true); pull/push keep
   the 2-minute network timeouts; audit events on every stage/unstage/
   commit/discard/branch action (discard + branch actions need NEW audit
   event types).
3. **Resilience**: conflicts land the repo in git's normal conflicted
   state — the app reports the conflict list and tells the user where
   resolution happens (Files/Terminal); the app never force-anything.
4. **Performance**: status/diff/log are runtime RPCs with the standard
   timeouts; lists lazy-render; nothing on the UI path parses git output
   client-side beyond what the server already ships as JSON.
5. **No silent clobbering**: every destructive action (discard, amend,
   checkout-with-dirty) requires an explicit confirmation naming the
   affected files.

## User stories / acceptance

- **US-SC1**: Make changes across two files on the machine, see both on
  the phone's Git tab grouped correctly; tap one → read its diff; stage
  both, write a message, commit; the web SCM shows the same staged→
  committed transition. Accept: state parity across clients.
- **US-SC2**: Commit with nothing staged → clear rejection; stage-all →
  everything moves to Staged; commit → staged group empties after
  refresh, count drops. Accept: flow is self-evident on the phone.
- **US-SC3**: Web edits a file while the phone's tab shows status →
  refresh on re-entry lists it. Accept: request/response refresh is
  enough, no live push.
- **US-SC4**: Pull on a phone behind upstream → fast-forward lands;
  pull into an already-diverged repo with conflicts → app shows the
  conflict list and does not pretend it resolved anything. Accept:
  conflicts visible, nothing auto-resolved.
- **US-SC5**: Push without upstream → branch published ("pushed new
  branch" surfaced); ahead/behind counts update from status. Accept:
  sync actions honest about what they did.
- **US-SC6 (stretch)**: Discard a modified file → file matches HEAD
  again, confirmed once by dialog naming the file; discard-all on a
  multi-file tree behaves per OQ-3's ruling. Accept: no silent loss.
- **US-SC7 (stretch)**: List branches, switch to another branch, commit;
  amend last commit changes its SHA on the web's log view. Accept:
  branch identity visible in the tab header at all times.

## Open questions (carry into the design pass — resolved there, not silently)

- **OQ-1 — Tab naming and icon**: "Source Control" per VSCode vs "Git"?
  (Space in the tab bar is tight; the web tab already calls it Source
  Control.)
- **OQ-2 — Diff rendering depth**: plain read-only unified diff (monospace
  scroll) in round 2, or side-by-side? (Round-1 Files preview precedent:
  keep one lane.)
- **OQ-3 — Discard semantics for untracked files**: delete them, refuse,
  or offer "delete untracked" as its own action? (`git restore` does not
  touch untracked files; `git clean` territory — recommend explicit
  opt-in, refuse by default.)
- **OQ-4 — Checkout with a dirty tree**: refuse outright, or dialog
  "commit/stash/discard first" (no stash support exists anywhere —
  refusal or guided paths only)?
- **OQ-5 — Amend guard**: refuse amend when the branch is not-behind its
  upstream (already published), or free-form with a loud warning?
- **OQ-6 — Auto-stage on commit**: mirror VSCode's "commit all" shortcut
  (stage-then-commit in one tap) or keep the staged-only rule strict?
- **OQ-7 — Where discard/branch/amend routes land in the protocol**:
  extend GitCommandPayload actions (likely) vs new payload types — a
  design decision, listed so it isn't forgotten.
- **OQ-8 — Auth split**: all 8 existing git routes need requireIdentity
  for the tab; new discard/branch/amend routes born with it. One slice
  or gradual? (Stage 4-5 precedent: per-slice.)
- **OQ-9 — Tab bar final form**: icons-only (FR-A.0) vs icons +
  selected-label; resolved by the on-device pass — ship icons-only
  first, revert to selected-label on user feedback. Icon shapes
  (terminal chevron, note/book, branch glyph) get user review before
  the slice commits.

## Explicitly out of scope (round 2)

Blame/annotations, inline gutter quick-diff decorations in editors,
stash, rebase/merge tooling, conflict resolution UI beyond listing,
remote management (add/remove), tags, submodules. These remain web/
terminal territory unless a later round pulls them in.