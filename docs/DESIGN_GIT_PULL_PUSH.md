# Design: Git Pull/Push

Status: Design. Implements `docs/REQUIREMENTS_NEW_FEATURES.md` §4.1.
ADR number below is proposed; final at the slice commit (next free slot
after ADR-045 Quick Commands).

## 0. Existing context the design builds on

- Source control already runs entirely as a `git.command`/`git.result`/
  `git.error` RPC triple over the existing runtime WebSocket connection
  (`internal/protocol/protocol.go:503-557`, `internal/api/git.go`,
  `internal/agent/git_manager.go`). Actions today: `status`, `diff`,
  `stage`, `unstage`, `commit`, `log`.
- The runtime shells the real `git` binary directly (`os/exec`, no shell,
  argv only — ADR-028) with `cmd.Dir` set to the Project's authoritative
  working directory, resolved server-side and never client-supplied
  (`internal/api/files.go:53` `fileRoot`).
- `internal/api/git.go`'s `gitCommand` helper already does the
  request/timeout/error-mapping boilerplate every handler reuses; pull/push
  handlers follow the same shape.
- Pull/push are the first git RPCs with real network I/O; every existing
  action is local and fast (bounded by `gitRPCTimeout` = `gitCommandTimeout`
  = 30s on both sides). Precedent for a longer-running RPC on the same
  transport: `dockerComposeRPCTimeout` = 11 minutes
  (`internal/api/docker.go:26`), vs. the ordinary `dockerRPCTimeout` = 15s.
- `web/static/project.html`'s Source Control panel header row
  (`#git-summary` + `#git-refresh-btn`, lines ~484-487) is the only part of
  the panel not hidden by the clean/dirty toggle in `loadGitStatus`
  (`#git-clean` vs. `#git-body`, lines ~2553-2589) — pull/push controls must
  live there.

---

## 1. Architecture overview

No new runtime capability manager, no new DB table, no new message
transport. This extends the existing `gitManager`/`git.command` surface
with two actions and extends `status`'s output with branch/tracking info.

```text
Browser ──REST── Control Server ──WS git.command── Runtime agent
  [Pull][Push]      internal/api/git.go              internal/agent/git_manager.go
  ↑2 ↓3 badge       (new handlers, same                (two new actions,
                     gitCommand() helper)               same runGit() exec)
                                                         ↓
                                                    git pull / git push
                                                    (ambient SSH agent /
                                                     credential helper on
                                                     the runtime host)
```

Auth: none of DevStationOne's business. `runGit` already just execs `git`
with `cmd.Dir` set — whatever credential helper, SSH agent, or `gh auth`
state the runtime process's environment already has is what pull/push
inherit, identically to running `git pull` by hand in that directory. No
new encrypted-secret surface, no change to `internal/agent`'s process
environment handling.

---

## 2. Protocol additions (`internal/protocol`, `docs/PROTOCOL.md`)

### 2.1 `GitCommandPayload.Action` gains two values

- `"pull"` — `Root` only. No path, no message.
- `"push"` — `Root` only.

No new payload fields needed for the request: pull/push always target the
current branch's configured (or auto-set) upstream, never a client-supplied
remote/branch name, per the security requirement in §4.1 of the
requirements doc (never trust a client-supplied branch/remote for anything
beyond display).

### 2.2 `status` gains branch/tracking fields

Extend `GitResultPayload` (`internal/protocol/protocol.go:552`) with an
optional `Branch *GitBranchStatus`, populated only for a `status` reply:

```go
// GitBranchStatus reports the current branch's upstream tracking state,
// parsed from `git status --porcelain=v2 --branch`'s header lines.
type GitBranchStatus struct {
    Name     string `json:"name"`               // current branch, "" if detached HEAD
    Upstream string `json:"upstream,omitempty"`  // e.g. "origin/main", "" if untracked
    Ahead    int    `json:"ahead"`
    Behind   int    `json:"behind"`
    HasRemote bool  `json:"has_remote"`          // `git remote` returned at least one entry
}
```

**Data source decision (resolves requirements doc's open question):**
extend the existing `status` action's own git invocation — add `--branch`
to `git status --porcelain=v2 -z -- .` (already run for every status
call) rather than a second `git rev-list --left-right --count` RPC. Porcelain
v2 `--branch` prepends header lines to the same output:

```
# branch.oid <sha>
# branch.head <name>
# branch.upstream <upstream>      (only if a tracking branch is set)
# branch.ab +<ahead> -<behind>    (only if a tracking branch is set)
```

One process spawn, one parse pass, no separate ahead/behind RPC, no new
endpoint. `has_remote` is a second cheap call (`git remote` with no args,
non-empty output) only needed to distinguish "no upstream" (branch never
pushed, push should offer to set one) from "no remote at all" (button
should render disabled) — both otherwise look identical from `--branch`
alone (`branch.upstream` absent in both cases).

### 2.3 `push` result

`GitResultPayload` needs one more optional field for push specifically:

```go
PushedNewUpstream bool `json:"pushed_new_upstream,omitempty"`
```

Set when the runtime had to auto-set upstream (branch had none), so the UI
can distinguish "pushed" from "published this branch for the first time" in
its status line without re-deriving it.

### 2.4 `pull` conflict result

Conflict is not a transport-level error — it's a normal git outcome the UI
must render (file list), not swallow into a generic 502. `GitResultPayload`
gains:

```go
Conflicts []GitFileStatus `json:"conflicts,omitempty"`
```

Reusing `GitFileStatus` (resolves the requirements doc's other open
question) rather than inventing a separate conflict shape: a merge conflict
is a path plus its unmerged porcelain-v2 XY code (`UU`, `AA`, `DD`, etc.),
exactly what `GitFileStatus.Index`/`Worktree` already represent for the
`u` (unmerged) status-line case `parseGitStatus` already parses
(`internal/agent/git_manager.go:230-233`). A `pull` that results in
conflicts is **not** an error reply (`git.error`) — it's a successful
`git.result` with `Conflicts` populated and `Files` empty, so the frontend's
existing result-vs-error branching doesn't need new plumbing; a genuinely
failed pull (no such remote, auth failure, network error) is still a
`git.error` exactly like today's other actions.

---

## 3. Runtime agent (`internal/agent/git_manager.go`)

### 3.1 `executeGit` new cases

```go
case "status":
    out, err := runGit(ctx, cmd.Root, "", "status", "--porcelain=v2", "-z", "--branch", "--", ".")
    // parseGitStatus already skips unrecognized leading tokens for '1'/'2'/'u'/'?';
    // add a case '#' that feeds parseGitBranch instead of being silently dropped.
    branch := parseGitBranch(out)
    branch.HasRemote = hasAnyRemote(ctx, cmd.Root)
    return protocol.GitResultPayload{Files: parseGitStatus(out), Branch: &branch}, nil

case "pull":
    out, err := runGit(ctx, cmd.Root, "", "pull", "--no-rebase")
    if isMergeConflict(err, out) {
        statusOut, _ := runGit(ctx, cmd.Root, "", "status", "--porcelain=v2", "-z", "--", ".")
        return protocol.GitResultPayload{Conflicts: unmergedOnly(parseGitStatus(statusOut))}, nil
    }
    if err != nil {
        return protocol.GitResultPayload{}, err
    }
    return protocol.GitResultPayload{}, nil

case "push":
    upstream, err := runGit(ctx, cmd.Root, "", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
    if err != nil {
        // no upstream — first push publishes the branch
        if _, err := runGit(ctx, cmd.Root, "", "push", "-u", "origin", "HEAD"); err != nil {
            return protocol.GitResultPayload{}, err
        }
        return protocol.GitResultPayload{PushedNewUpstream: true}, nil
    }
    if _, err := runGit(ctx, cmd.Root, "", "push"); err != nil {
        return protocol.GitResultPayload{}, err
    }
    return protocol.GitResultPayload{}, nil
```

No `--force` anywhere — matches the requirements decision; a force-push is
simply not an action this RPC surface offers.

`--no-rebase` on pull is explicit rather than relying on the runtime host's
`pull.rebase` git config, so behavior is identical across dev machines
regardless of each user's global git config — this is a product decision
(merge, not rebase, per the requirements doc's "fail loud, no auto-merge
surprises" framing) not a git default to inherit.

### 3.2 Conflict detection

`git pull` exits non-zero on conflict, same as any other failure — `err !=
nil` alone doesn't distinguish "conflict" from "no remote"/"auth failure"/
"network unreachable". `isMergeConflict` inspects the captured stderr text
for git's own conflict markers (`"CONFLICT"`, `"Automatic merge failed"`) —
the same string-sniffing precedent already exists nowhere else in this
codebase, so this is new surface; call out as a review point (a git
version/locale change could alter the message — acceptable risk since a
misclassified conflict just surfaces as a generic error instead, not a
silent data-loss path).

### 3.3 Timeout

New constant `gitNetworkCommandTimeout = 2 * time.Minute` for `pull`/`push`
specifically (vs. `gitCommandTimeout` = 30s for the local-only actions) —
matches the `dockerRPCTimeout` vs. `dockerComposeRPCTimeout` split already
in the codebase for the same reason (network-bound vs. local-bound RPC on
one transport). `internal/api/git.go`'s `gitRPCTimeout` needs a matching
longer constant for the same two actions, or the Control Server times out
the HTTP request before the runtime's own operation would.

---

## 4. Control Server (`internal/api/git.go`, `internal/api/server.go`)

### 4.1 New handlers

```go
func gitPullHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc
func gitPushHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc
```

Same shape as `gitCommitHandler`: resolve `conn, root` via `fileRoot`, call
`gitCommand` with the longer timeout, map the result. `gitPullHandler`
branches on `result.Conflicts` to choose the response shape (200 clean vs.
200 with a conflicts array — not an error status; see §2.4). `gitPushHandler`
returns `{pushed_new_upstream: bool}`.

### 4.2 Routes (`internal/api/server.go`, after line 162)

```go
mux.HandleFunc("POST /api/v1/projects/{projectID}/git/pull", requireSession(db, gitPullHandler(db, registry)))
mux.HandleFunc("POST /api/v1/projects/{projectID}/git/push", requireSession(db, gitPushHandler(db, registry)))
```

### 4.3 Audit events (`internal/audit/audit.go`, after `EventGitCommitted`)

```go
EventGitPulled = "git.pulled"
EventGitPushed = "git.pushed"
```

Recorded the same way `gitCommitHandler` records `EventGitCommitted` —
resource_id is the branch name (from the `status` response the frontend
already has), detail carries `{project_id}` plus, for pull, whether it
resulted in conflicts.

### 4.4 OpenAPI (`docs/openapi.yaml`)

Two new paths after `git/commit`, matching the existing git path style
(`GitFileStatus` schema reused for `conflicts`):

- `POST /api/v1/projects/{projectID}/git/pull` → `200 {conflicts: GitFileStatus[]}` (empty array = clean pull), `409`/`502`/`504` as existing.
- `POST /api/v1/projects/{projectID}/git/push` → `200 {pushed_new_upstream: bool}`, same error set.

`GitFileStatus`'s existing description ("One changed path from a git status
call") needs a one-clause addition noting it doubles as a conflict entry
for `git/pull`.

`git/status`'s response schema changes from a bare array to
`{files: GitFileStatus[], branch: GitBranchStatus}` — **this is a breaking
response-shape change** for an endpoint the frontend already calls
(`loadGitStatus`, `project.html:2558`). Both the handler and the frontend
parser change in the same slice; no versioned/parallel endpoint, since this
API has no external consumers yet (control server + its own bundled UI
only) per `docs/API.md`'s versioning stance elsewhere. New `GitBranchStatus`
schema component added alongside `GitFileStatus`/`GitCommitInfo`.

---

## 5. Frontend (`web/static/project.html`)

### 5.1 Markup — header row (replacing lines ~484-487)

```html
<div class="d-flex justify-content-between align-items-center mb-2">
  <div id="git-summary" class="text-secondary">—</div>
  <div class="btn-list gap-1 align-items-center">
    <span id="git-branch-status" class="text-secondary small"></span>
    <button id="git-pull-btn" class="btn btn-sm" disabled>Pull</button>
    <button id="git-push-btn" class="btn btn-sm" disabled>Push</button>
    <button id="git-refresh-btn" class="icon-btn" aria-label="Refresh source control" title="Refresh"><i class="fa-solid fa-rotate" aria-hidden="true"></i></button>
  </div>
</div>
```

Both buttons start `disabled`; `loadGitStatus`'s `.done()` handler enables
them from `branch.has_remote` regardless of the `files.length` early return
at line 2563-2568 — that early `return` happens **before** the branch UI
update, so the branch/pull/push rendering call must move above it (or be
duplicated on both paths), otherwise pull/push silently stop updating the
moment the tree goes clean, reproducing exactly the bug the requirements
pass flagged.

### 5.2 `git-branch-status` rendering

`"↑2 ↓3"` from `branch.ahead`/`branch.behind` when both present; `"no
tracking branch"` when `branch.upstream` is empty but `has_remote` is true;
nothing rendered (buttons stay disabled, tooltip explains) when
`has_remote` is false.

### 5.3 Pull/push click handlers

Same pattern as `#git-commit-btn`'s click handler (lines 2595-2609):
disable the clicked button, POST, re-enable, `loadGitStatus()` on success to
refresh everything (files, diff, branch badge) in one round-trip — no
separate conflict-specific UI state beyond rendering `result.conflicts` (if
non-empty) as a plain list in `#git-error`-style alert with a note to
resolve via Terminal, per the requirements doc's decision not to build
in-browser conflict resolution in this slice.

---

## 6. Security review against `docs/SECURITY.md` / `CLAUDE.md` §Security

- Remote/branch never client-supplied — pull/push always act on `HEAD`'s
  current upstream (or `origin`/`HEAD` for the first-push case), resolved
  server/runtime-side, matching the existing rule for `Root`.
- No new credential storage — ambient auth only, explicitly decided in the
  requirements pass; nothing to encrypt, nothing to log. `runGit` already
  never logs command output.
- No `--force` surface at all — the highest-risk git operation (destructive
  to remote history) is simply not exposed.
- Push/pull remain gated by the same `requireSession` + Project-membership
  check every other git route uses; no new authorization logic needed.

---

## 7. Open items for `/sc:workflow` / implementation

- Confirm `git status --porcelain=v2 --branch` header-line parsing doesn't
  collide with `parseGitStatus`'s existing token-type switch (`'1'`, `'2'`,
  `'u'`, `'?'`) — header lines start `#`, currently silently dropped by the
  `for` loop's lack of a matching case, so this is additive, not a
  behavior change to verify against existing tests.
- `docs/PROTOCOL.md` "Implementation (Slice 16): git commands" section
  needs its action list and payload shape doc updated in the same slice
  (`CLAUDE.md` step 21), not deferred.
- New Go unit tests: `parseGitBranch` (all four header-line combinations:
  no upstream, tracking with 0/0, ahead-only, behind-only, both), the `'#'`
  addition to `parseGitStatus`'s dispatch not breaking existing status
  parsing tests, `isMergeConflict` string-matching against real `git pull`
  conflict output (integration test, real repo fixture, real conflicting
  commit — no mocking the git binary itself per this project's "production
  functionality must be real" rule).
