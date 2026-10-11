# DESIGN_ANDROID_SCM.md — Android Source Control tab (round 2)

Implements `docs/REQUIREMENTS_ANDROID_SCM.md` (OQ-1..OQ-9 resolved
2026-10-10). One backend slice + three Android slices; every new route is
born with `requireIdentity`, every existing git route switches in the same
backend slice (OQ-8). Established precedents reused verbatim: ADR-028
(real git CLI on the runtime, argv-only), ADR-053 addendum auth switching
(`TestDeviceFilesAuth` test pattern), per-project `fileRoot`, 30 s local /
2 min network RPC timeouts, and round-1's `hiddenOther` workspace pattern.

## 1. Verified decisions

- **Material3 tabs, icon-only variant** (context7 reference 2026-10-10):
  `Tab(selected, onClick, icon = { … })` carries the icon-only overload;
  `TabRow` itself is deprecated in favor of `PrimaryTabRow` — the
  workspace migration rides this round's tab slice, not a separate
  refactor. A11y labels ride `Icon(contentDescription = …)`; no tooltip
  machinery (round-2 keeps it lean).
- **OQ-7 (protocol shape)**: resolved as **`GitCommandPayload.Action`
  extensions** — discard, amend, branches, checkout, branch. No new
  payload types; `GitResultPayload` gains one field (`Branches`). Rationale:
  the envelope is a discriminated-by-action string already, `path` /
  `message` / `root` fields cover every new operation, and the runtime's
  `executeGit` switch is the established extension point (ADR-028).
- **OQ-5 enforcement lives in the runtime**: the app always SHOWS the
  amend affordance; the runtime is the single authority that refuses
  ("amend refused: commits already pushed") via the standard git.error
  path. One authority prevents app-heuristic/runtime-truth drift. The
  check is `git merge-base --is-ancestor HEAD @{u}` (HEAD is an ancestor
  of upstream ⇒ published).
- **OQ-3/OQ-4 guards execute in the runtime too**: discard on an
  untracked path errors ("refusing to discard an untracked file");
  checkout with a dirty tree errors naming the dirty count ("working
  tree is dirty — commit or discard first"). The UI disables/labels, the
  runtime enforces.

## 2. Component map

```text
android/app/src/main/java/dev/devstationone/
├── net/Api.kt                + GitFileStatus/GitBranch/GitCommitInfo models;
│                             status/diff/stage/unstage/commit/pull/push/log
│                             + discard/amend/branches/checkout/branch (create)
├── ui/ScmScreen.kt           (new: the Source Control tab body — branch header,
│                             grouped status list, actions row, commit box,
│                             history list, branch sheet; diff overlay lives here too)
├── ui/Workspace.kt           4th tab; TabRow → PrimaryTabRow icon-only
└── ui/kit/Kit.kt             + MuxIcons.Prompt / Note / Branch (round-2 set;
                              Terminal/Journal/SCM) — artwork user-reviewed
                              before the slice commits; Folder reused for Files

Control Server + runtime (backend slice):
├── internal/protocol/protocol.go     action strings + GitBranchInfo + Branches field
├── internal/agent/git_manager.go     discard/amend/branches/checkout/branch actions
├── internal/api/git.go               4 new handlers + shared guards wiring
├── internal/api/server.go            8 existing routes → requireIdentity; 5 new routes
├── internal/audit/audit.go           git.discarded / git.amended / git.branch_created /
│                                     git.checkout
└── docs/openapi.yaml                 deviceSig entries on all git routes; 5 new
                                      endpoints; GitBranch + Commit-amend schemas
```

## 3. Protocol (OQ-7 resolution, concrete)

New `GitCommandPayload.Action` values (Root required as today):

| Action | Fields consumed | Runs (`git`, argv-only) | Result fields |
|---|---|---|---|
| `discard` | Path (required) | `git restore -- <p>`; refuses when the porcelain-v2 status has `?` in Worktree for the path ("untracked — nothing to discard") and when the path is clean ("nothing to discard") | none |
| `amend` | Message (stdin) | guard: upstream exists && `git merge-base --is-ancestor HEAD @{u}` → error; else `git commit --amend -F -`; reply SHA | `commit` |
| `branches` | — | `git for-each-ref refs/heads --format=%(HEAD)x1f%(refname:short)x1f%(upstream)` (+ remotes, display-only, second section) | `branches` |
| `checkout` | Path = branch name | refuse when `git status --porcelain=<v2>` records any change ("working tree is dirty…"); `git checkout -- <name>` | none |
| `branch` | Path = branch name | `git checkout -b <name>` (creates and switches); the name is a single argv token so shell injection is impossible by construction (no shell exists — ADR-028) | none |

`GitResultPayload` gains `branches []GitBranchInfo` where
`GitBranchInfo{name, current bool, upstream}` — parsing lives next to
`parseGitBranch`/`parseGitLog` with the same `%x1f` conventions.

### HTTP routes (all `requireIdentity`, all `deviceSig` in openapi)

| Route | Handler | Replies | Audit |
|---|---|---|---|
| `POST …/git/discard` | `{"path"}` | 204 | `git.discarded` |
| `POST …/git/amend` | `{"message"}` | 200 `{"commit": sha}` | `git.amended` |
| `GET …/git/branches` | — | 200 `[GitBranchInfo]` | — (read-only) |
| `POST …/git/checkout` | `{"name"}` | 204 | `git.checkout` |
| `POST …/git/branch` | `{"name"}` | 204 | `git.branch_created` |

Existing 8 routes (`status/diff/stage/unstage/commit/log/pull/push`) keep
their contracts; only the middleware switches. Commit-vs-amend stays two
routes (semantics differ by guard; the web consumes neither new one yet —
the routes land openapi-described for it to adopt later, FR-C).

## 4. Screen anatomy (ScmScreen)

```text
Branch header     ● main · origin/main · ↑2 ↓1        [branch ▾]  (sheet)
Actions row       [Refresh] [Pull] [Push]
Changes groups    STAGED (n)          row: path (+ A/M/D/R chip), tap → diff
                  CHANGES (n)         row: path (+ chip, orig_path → renames)
                  (untracked rows render inside CHANGES with a "?" chip)
Commit box        multiline field · [Commit] [Commit all] ([Amend…] when a
                  commit history exists) — empty message refused client-side
                  (the runtime refuses again — one UX, one truth)
History           most recent N commits (sha7 · subject · author · when)
Diff overlay      full screen like NoteScreen: path + [Staged?] toggle at top,
                  monospace unified diff, +/- tinted lines, horizontal scroll
Branch sheet      local branches (current ✓), upstream muted, remotes
                  display-only; "New branch…"; checkout disabled while the
                  latest status shows any change ("commit or discard first")
```

- State model: `status` payload held in the screen; grouping is a unit-tested
  pure mapping (XY → Staged/Changed/Untracked/Renamed) — never re-parsed
  from git text on the client. Mutating actions re-fetch status on completion.
- Refresh: on tab-first-activation, explicit Refresh, and after every
  mutating action. No sockets, no file-watch (request/response round only —
  US-SC3 acceptance is "refresh is enough").
- Pull conflicts: the app never parses them into resolution UI; a dialog
  lists conflicted paths and points at Files/Terminal. Push surfaces
  "published this branch" from `pushed_new_upstream`.
- The tab stays composed while hidden (`hiddenOther`), matching Journal/Files.

## 5. Danger rules (mirrors the requirements, restated as implementation)

- Discard and amend each show a confirm dialog naming the file(s); the
  runtime re-checks the same conditions server-side — the dialogs are UX,
  not the security boundary.
- Checkout disabled while dirty (app greys; runtime refuses).
- Amend refusal text is the runtime's ("amend refused: these commits are
  already on the remote"); the app surfaces it verbatim.
- No action ever spawns a shell; every git argv is a direct exec argument
  (ADR-028), branch names included — they are single argv tokens.

## 6. Testing

- Runtime (`internal/agent/git_manager_test.go`): discard on tracked-modified
  → restored; discard untracked → refusal; discard clean → refusal; amend
  after commit (no upstream) → SHA changes; amend when upstream contains
  HEAD → refusal; checkout clean → branch switches; checkout dirty →
  refusal; branch create → branch exists + checked out; branches list →
  current marker + upstream fields. All against real git in the test env
  (ADR-028's existing test harness).
- API (`internal/api/git_*` + device auth): per new route, owner-device /
  stranger-device / anonymous; the stranger assertion rides the
  shared-projects model like `TestDeviceFilesAuth`. Existing git route
  device-auth covered in the same table-driven style.
- Android: grouping mapping unit test; amend-offer/pull-push disabled
  derivation test (`ScmScreenLogicTest`).
- E2E: Pixel release APK vs prod (route switches shipped before device
  E2E — the stage-4 lesson, recorded).

## 7. Slices (each vertical: code + tests + openapi + docs + CodeGraph + commit)

1. **Backend: git actions + device identity** — protocol, runtime actions,
   routes, audit events, openapi, `requireIdentity` on ALL 13 git routes,
   tests; deploy dev + prod (`scripts/deploy-to-prod.sh`) before any
   device work. Accept: route probes + full `make test` green; web
   unchanged.
2. **Tab bar icons + SCM core** — `PrimaryTabRow` icon-only (4 tabs,
   artwork pre-reviewed), `ScmScreen` status/diff/stage/unstage/commit/
   commit-all/pull/push/log. Accept: US-SC1..US-SC5 minus stretch items;
   device E2E.
3. **Discard + amend + branch sheet** — UI over slice 1's backend, with
   confirm dialogs and refusals surfaced verbatim. Accept: US-SC6, US-SC7.
4. **Docs + roundoff** — IMPLEMENTATION_PLAN slice entry, ANDROID_CLIENT
   scope, REQUIREMENTS marked shipped, on-device E2E record, CodeGraph.

## 8. Risks

| Risk | Mitigation |
|---|---|
| Amend guard mis-answers on unborn/odd repo states (no HEAD, no upstream, bare?) | Runtime error path returns git's own stderr text; app surfaces verbatim; runtime unit tests cover no-upstream and no-HEAD repos |
| Discard on a rename or directory path (`git restore` semantics) | Only per-file paths from the status list are offered; dirty/untracked refusals catch the rest; path is `filesystem.Clean`ed as every other route does |
| Long diffs on phones (multi-MB unified diff) | LazyColumn line rendering is memory-bounded by viewport (Compose text recycling); no syntax highlighting on diffs |
| Icon-only tabs unclear on device | OQ-9 fallback documented: switch to icon + selected-label; artwork pre-reviewed before the slice |
| Porcelain-v2 parse drift between status and grouping | Single server-side JSON contract consumed by both clients; grouping unit-tested in the app; no client-side git text parsing |

## 9. Status

- Design complete 2026-10-10; OQ-1..OQ-9 closed
  (`docs/REQUIREMENTS_ANDROID_SCM.md`), OQ-7 resolved here (§1/§3).
- Slice 1 not started. The Android side inherits the stage-1..5
  component/style precedents; no new patterns are introduced beyond the
  ones recorded above.