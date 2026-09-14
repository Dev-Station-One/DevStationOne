# Requirements — New Features (brainstorm pass, 2026-09-14)

Requirements discovery only. No architecture or implementation decisions here —
see `/sc:design` and `/sc:workflow` for next steps per feature.

Deep pass covered: Dashboard rewrite, Notifications, Alternate HTML terminal,
Git client. Deferred (not explored yet): DB backup/restore, Project
backup/restore, DB schema editor.

## 1. Dashboard rewrite

**Goal:** replace near-empty `web/static/index.html` shell (sidebar + theme
toggle only, no content) with a live overview.

**Functional requirements:**
- Active Projects: all projects the user owns, recency-sorted, with a status
  badge (running / idle / runtime offline).
- Runtime status: list of registered runtimes with live health, updated via
  WebSocket push (reuse existing runtime connection/WS gateway — no new
  polling loop).

**Non-functional requirements:**
- Initial paint from a REST snapshot, then WS diffs for live updates.
- Must degrade gracefully (reconnect/backoff) if the WS connection drops,
  mirroring the existing terminal reconnect pattern.

**Acceptance criteria:**
- Opening the dashboard shows current state with no manual refresh required.
- A runtime going offline updates its badge within the WS heartbeat interval,
  not only on next page load.

## 2. Notifications (bell)

**Goal:** Claude-Code-style bell notification for task completion,
awaiting-input, and system events.

**Functional requirements:**
- Triggers: background task completion, a session awaiting input, and
  runtime/system events (disconnect, service crash, auth/security events).
- Awaiting-input detection: idle-time heuristic — no PTY output for N seconds
  after prior activity flags the session as "awaiting input." Generic across
  tools; accepts false positives as a tradeoff for not requiring per-tool
  integration.
- Persisted server-side in Postgres, per-user. Survives reload, reconnect,
  and login from another device. Unread count shown as a bell badge.

**Open questions:**
- Idle threshold N is untuned — expect false positives on long-running silent
  commands (e.g. compiles, large file transfers).
- Needs a per-session mute/dismiss mechanism to suppress noisy sessions.

**Acceptance criteria:**
- A notification fires and appears in the bell dropdown after reloading on a
  second device.
- Unread count clears when the notification is read.

## 3. Alternate HTML terminal (forms-based)

**Goal:** a second terminal type, selectable per session, alongside the
existing xterm.js terminal (`web/static/project.html`, `#terminal-canvas`),
which remains the default/primary terminal type and is not modified. The new
type renders a structured tool-call UI — cards for diffs, file changes, and
permission prompts with clickable form-action buttons — instead of raw ANSI
text.

**Scope decision:** generic across CLI tools, not limited to Claude Code.

**Major open risk:** there is no stable machine-readable output contract for
arbitrary CLI tools. Claude Code supports `--output-format stream-json`,
which would make structured parsing reliable for that one tool. Generic CLIs
only emit ANSI text/escape sequences, meaning this requires screen-scraping —
fragile, breaks whenever the target tool's output format changes, and carries
an open-ended maintenance cost. This is the highest-risk item in this
requirements pass. Recommend a design spike (`/sc:design`) focused
specifically on parsing feasibility before committing engineering time, or
revisiting the decision to scope this to Claude Code first and generalize
later.

**Functional requirements:**
- User selects terminal type per session (existing PTY/xterm vs. new
  forms-based type).
- Forms-based terminal renders parsed output as structured cards; prompts
  requiring a response are rendered as form controls (buttons/inputs) that
  send the equivalent input back over the PTY.

**Acceptance criteria:**
- A session started in "forms" mode renders at least a file-diff card and a
  yes/no-prompt card as structured UI for one reference CLI tool.
- The existing xterm terminal type is unchanged in behavior.

## 4. Git client (VSCode Source Control-style)

**Goal:** a source-control panel with full parity: status, stage/unstage,
commit, push/pull/fetch, branch switch, merge conflict resolution,
interactive rebase, commit history graph, and stash.

**Execution model:** runtime-side only. Git commands execute on the
Development Runtime machine where the repository actually lives; the Control
Server relays commands and results over the existing runtime connection —
consistent with how the Files and Terminal features already work. The
Control Server does not clone or mirror the repository itself.

**Authentication:** uses the runtime's existing local git config, SSH keys,
and credential helper. DevStationOne stores no git credentials and
introduces no new encrypted-secret surface for this feature.

**Security requirements:**
- Repository path must be resolved server-side against the Project's
  authoritative working directory — never accepted as a client-supplied
  path, matching the containment rule already required for the Files
  feature (`docs/SECURITY.md`).

**Scope note:** "full parity including merge/rebase/history" is a large
surface. This is very likely multiple implementation slices rather than one:
(a) status/diff/stage/commit, (b) branch/push/pull/fetch, (c) commit history
graph, (d) merge/rebase/stash UI. Slice boundaries to be finalized at
`/sc:design` or `/sc:workflow` time.

**Acceptance criteria:**
- Opening a repo with uncommitted changes shows the changed-file list and
  diffs.
- Staging, committing, and pushing from the panel lands the commit on the
  actual remote, authenticated via the runtime's own git identity/keys.

## Deferred features (not explored this pass)

- Database backup/restore
- Project backup/restore
- Database schema editor (replacing the current database view)

Each needs its own `/sc:brainstorm` pass before design/implementation.
