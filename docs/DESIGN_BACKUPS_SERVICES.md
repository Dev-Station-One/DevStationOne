# Design: Backups and Runtime-Level Services

Status: Design. Implements the requirements in
`docs/REQUIREMENTS_BACKUPS_SERVICES.md`. ADR numbers below are proposed; they
become final in the slice commits that introduce them (next free slot is
ADR-041, after ADR-040 Docker/Compose).

## 0. Existing context the design builds on

- Database connections are **project-scoped** rows with AES-encrypted secrets
  at rest; the **runtime** opens and caches the actual connections
  (`db.command`, per-connection pool cache with `invalidate`).
- The runtime agent dispatches control-plane messages by type to capability
  managers (`internal/agent/dispatcher.go`): session, service, file, db, git,
  edit, docker. A new capability is a new manager + message type.
- All RPCs are correlated request/reply envelopes
  (`runtimes.Conn.SendCommand`); long operations run in a goroutine on the
  agent and reply asynchronously.
- File read/write today is whole-file-in-envelope (`MaxFileBytes` = 24 MiB) —
  inadequate for backups, hence chunked transfer (below).
- Filesystem containment lives in `internal/filesystem` (os.Root based);
  restore extraction must reuse its validation approach.
- Control Server resolves "project → runtime connection" via
  `runtimeConnForProject`; runtime-scoped handlers will resolve runtime
  directly.

---

## 1. Architecture overview

Three new/changed pieces:

1. **Runtime agent: `backupManager`** — new capability handling
   `backup.command`: create backups (DB dumps, project archives), verify and
   restore them, list/delete stored files, and serve chunked reads for
   download. Long operations are jobs; progress is pushed as `backup.event`
   envelopes and pollable via `backup.command {action: "status"}`.
2. **Control Server: backups package + scheduler** — `internal/backups`
   owns metadata tables (Postgres), an API surface, a download proxy that
   chunks through the runtime, and a scheduler goroutine that fires due
   schedules.
3. **Services relocation** — project-scoped service routes/UI removed;
   runtime-scoped routes added over the same `service.command` protocol.

```text
Browser ──REST── Control Server ──WS envelope── Runtime agent
                    │  backups metadata (Postgres)
                    │  scheduler goroutine (fires due schedules)
                    └── backup files live ONLY on runtime host:
                        ~/.local/share/devstationone/backups/{db,projects}/
```

---

## 2. Protocol additions (`internal/protocol`, `docs/PROTOCOL.md`)

### 2.1 Message types

- `backup.command` (server → runtime, request)
- `backup.result` (runtime → server, reply)
- `backup.error` (runtime → server, reply on failure)
- `backup.event` (runtime → server, unsolicited job progress)

### 2.2 Payloads

```go
type BackupCommandPayload struct {
    Action string `json:"action"` // start, status, list, delete, read, restore

    // start (DB): which connection + engine params (same Params struct the
    // existing db.command carries, including decrypted secret).
    Params *dbconnectors.Params `json:"params,omitempty"`

    // start (project): absolute working directory to archive.
    SourceDir string `json:"source_dir,omitempty"`

    // restore: which stored backup, and where.
    BackupPath string `json:"backup_path,omitempty"`
    RestoreDir string `json:"restore_dir,omitempty"` // target project dir (new or existing)

    JobID      string `json:"job_id,omitempty"`       // status
    BackupID   string `json:"backup_id,omitempty"`    // delete/read/list scoping
    Offset     int64  `json:"offset,omitempty"`       // read
    Length     int    `json:"length,omitempty"`       // read (chunk size, 4 MiB)
}

type BackupResultPayload struct {
    JobID      string          `json:"job_id,omitempty"`
    BackupID   string          `json:"backup_id,omitempty"` // start: assigned ID
    Size       int64           `json:"size,omitempty"`
    State      string          `json:"state,omitempty"`     // running|done|failed
    Error      string          `json:"error,omitempty"`
    Backups    []BackupInfo    `json:"backups,omitempty"`   // list
    Data       []byte          `json:"data,omitempty"`      // read chunk (base64 in JSON envelope)
}

type BackupEventPayload struct { // backup.event push
    JobID  string `json:"job_id"`
    State  string `json:"state"`  // running|done|failed
    Error  string `json:"error,omitempty"`
    Size   int64  `json:"size,omitempty"`
}
```

Rules:

- `start` replies immediately with a `JobID` and `BackupID` (runtime picks the
  backup filename and pre-registers it); the actual dump/archive runs in a
  goroutine. The Control Server learns completion via `backup.event`; the
  `status` action exists as a poll fallback after reconnect (events can be
  lost on disconnect — the metadata row reconciles from `status` or from the
  file's presence on a later `list`).
- `read` is synchronous chunked transfer: the Control Server issues sequential
  `read` requests walking offsets; 4 MiB per chunk keeps every envelope far
  under `MaxMessageBytes`.
- The runtime never reports file contents in logs (SECURITY rule); chunk data
  flows only in envelopes.

### 2.3 Runtime-side job model (`internal/agent/backup_manager.go`)

- Job map `jobID → {state, error, size}`, mutex-guarded, like `serviceManager`
  but with longer timeouts (dumps can be minutes; no per-job timeout kill —
  cancellation comes from a `cancel` action, added if ever needed; MVP has
  none).
- DB dump commands per engine (executed on runtime host, stderr captured for
  the error message only, never the dump data):
  - PostgreSQL: `pg_dump --dbname=<conninfo> -F custom -f <out>`
  - MySQL/MariaDB: `mysqldump --single-transaction ...` (mariadb-dump
    preferred when present)
  - MSSQL: `sqlcmd -Q "BACKUP DATABASE ..."` (bcp fallback decided in slice)
  - SQLite: `.backup` via `sqlite3` binary (safe copy; no plain cp of a live
    db)
- Project archive: tar.gz of the working directory (every entry, no
  `.gitignore` handling) + sidecar manifest `devstation.json` written to the
  backups root as two files per backup:
  `<id>.tar.gz` and `<id>.json`.
- Restore (project, overwrite mode): runtime closes nothing itself — the
  Control Server closes sessions first (§5); the runtime takes a safety
  snapshot of the current directory (same archive+manifest format,
  `trigger=pre_restore`), then wipes directory contents and extracts,
  validating every entry with the same containment rules as
  `internal/filesystem.Clean` (reject `..`, absolute paths, and any entry
  that would escape the target root; symlinks are extracted as symlinks but
  never followed outside the root).
- Manifest validation before restore: `version` field must match a supported
  format version; file count is advisory, not enforced.

---

## 3. Control Server design

### 3.1 Package and tables (`internal/backups`, migration `NNNN_backups.sql`)

```sql
CREATE TABLE backups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('db','project')),
    -- exactly one of these is set, per kind:
    connection_id UUID REFERENCES database_connections(id) ON DELETE CASCADE,
    project_id    UUID REFERENCES projects(id)             ON DELETE CASCADE,
    runtime_id  UUID NOT NULL REFERENCES runtimes(id)      ON DELETE CASCADE,
    file_name   TEXT NOT NULL,        -- path relative to the runtime backups root
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    trigger     TEXT NOT NULL CHECK (trigger IN ('manual','schedule','pre_restore')),
    state       TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running','done','failed')),
    error       TEXT,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- CHECK: (kind='db' AND connection_id IS NOT NULL AND project_id IS NULL)
--     OR (kind='project' AND connection_id IS NULL AND project_id IS NOT NULL)

CREATE TABLE backup_schedules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('db','project')),
    connection_id UUID UNIQUE REFERENCES database_connections(id) ON DELETE CASCADE,
    project_id    UUID UNIQUE REFERENCES projects(id)             ON DELETE CASCADE,
    cadence     TEXT NOT NULL CHECK (cadence IN ('hourly','daily','weekly')),
    retention   INT  NOT NULL DEFAULT 10 CHECK (retention BETWEEN 1 AND 100),
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    last_result TEXT CHECK (last_result IN ('success','failed','skipped_offline')),
    last_error  TEXT,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

One schedule per database connection / per project (`UNIQUE` per kind). The
same `backups` row types serve DB and project; `kind` discriminates.

### 3.2 Scheduler

- One goroutine in the server process (started with the HTTP server), ticking
  every 30 s: `SELECT ... WHERE next_run_at <= now() FOR UPDATE SKIP LOCKED`.
- For each due schedule: set `next_run_at = now() + cadence` immediately
  (crash-safe; a failed run simply records `last_result='failed'`). If the
  target runtime is offline, record `last_result='skipped_offline'` and move
  on — no queueing (requirements: missed runs skipped, documented).
- Otherwise resolve the project (or the connection's project) → runtime
  connection → issue `backup.command start` with `trigger='schedule'`; update
  `last_run_at` at fire time and `last_result` when the job finishes
  (via `backup.event` handling in the same package).
- Retention: after each successful scheduled backup, delete the
  (retention)-th oldest older `trigger='schedule'` backup of the same
  schedule — metadata row + `backup.command delete` on the runtime, in that
  order; an orphaned file with no metadata row is cleaned up by the runtime's
  `list`-based reconciliation is NOT attempted in MVP (documented).

### 3.3 API surface (OpenAPI additions)

Backups (DB, under the existing project-scoped connection):

```
GET    /api/v1/projects/{projectID}/db-connections/{connectionID}/backups
POST   /api/v1/projects/{projectID}/db-connections/{connectionID}/backups          (manual trigger)
GET    /api/v1/projects/{projectID}/db-connections/{connectionID}/backups/{backupID}
DELETE /api/v1/projects/{projectID}/db-connections/{connectionID}/backups/{backupID}
GET    /api/v1/projects/{projectID}/db-connections/{connectionID}/backups/{backupID}/download
PUT    /api/v1/projects/{projectID}/db-connections/{connectionID}/backups/schedule
DELETE /api/v1/projects/{projectID}/db-connections/{connectionID}/backups/schedule
```

Project backups:

```
GET    /api/v1/projects/{projectID}/backups
POST   /api/v1/projects/{projectID}/backups
GET    /api/v1/projects/{projectID}/backups/{backupID}
DELETE /api/v1/projects/{projectID}/backups/{backupID}
GET    /api/v1/projects/{projectID}/backups/{backupID}/download
POST   /api/v1/projects/{projectID}/backups/{backupID}/restore
PUT    /api/v1/projects/{projectID}/backups/schedule
DELETE /api/v1/projects/{projectID}/backups/schedule
```

`POST .../restore` body: `{"mode": "new" | "overwrite"}`. `overwrite` targets
the backup's own project and requires the body to repeat the project's
current name verbatim (`{"confirm_name": "..."}`) — server-side gate per
NFR-PJ-1/FR-PJ-5, not just a UI checkbox. `new` restore creates the project
record itself (name = `<name> restored <date>`, sanitized, collision-suffixed
working directory under the target runtime root; target runtime = the
backup's runtime, changeable while online).

Download proxy: handler streams `read` chunks to the browser with
`Content-Length` set from `size_bytes`; `state != 'done'` backups are not
downloadable.

Services (runtime-scoped; project-scoped equivalents deleted):

```
GET  /api/v1/runtimes/{runtimeID}/services
GET  /api/v1/runtimes/{runtimeID}/services/{unit}
POST /api/v1/runtimes/{runtimeID}/services/{unit}/{action}   (start|stop|restart|enable|disable)
GET  /api/v1/runtimes/{runtimeID}/services/{unit}/logs?lines=
```

Authorization: same rule the runtimes pages already use — any authenticated
user may act on an enrolled runtime (services are runtime-host resources, not
per-project data; audit records carry runtime+unit). If the runtimes area
later gains per-user ACLs, these endpoints inherit them — single source of
truth, no separate check here.

### 3.4 Session closing before overwrite-restore (FR-PJ-8)

The restore handler, before issuing `restore` with overwrite mode:
1. List the project's open sessions (existing sessions store).
2. Send `session.close` for each over its runtime connection (existing
   protocol message), wait for `session.exit`/`session.closed` events with a
   5 s grace per session; on timeout, proceed anyway (runtime kills on
   directory replacement risk is accepted and logged).
3. Then issue the `restore` command.

### 3.5 What gets deleted

- `internal/api/services.go` handlers re-registered under
  `/api/v1/runtimes/{runtimeID}/services/...`; `runtimeConnForProject` is
  replaced by a runtime-authorized variant. The project Services tab and its
  sidebar entry are removed from the web UI; `serviceCommand` protocol
  payload is unchanged (runtime-side `execute` untouched).
- Old project service routes: removed from `server.go` and OpenAPI. No
  compat shim (ADR-043).

---

## 4. UI design

- **Databases tab (project)**: per-connection drawer/section gains a
  "Backups" panel — list (created, size, trigger, state), "Back up now",
  download, delete, restore dialog (target DB dropdown on the connection +
  new-DB name field; typed confirmation when target exists), and a schedule
  row (cadence, retention, next/last run + result, edit/remove).
- **Project workspace**: new "Backups" tab — list, "Back up now", download,
  delete, restore (new-project vs overwrite with `confirm_name` typed
  dialog), schedule config. The Services tab is removed from the workspace.
- **Runtimes area**: runtime detail gains a "Services" view listing all user
  units with the existing status/actions/log viewer components, reused
  as-is.
- Long jobs: backup buttons go into a pending state; the list row shows
  `running` (poll every 2 s while any row is `running` — no push channel to
  the browser in MVP).
- All per `docs/WEB_UI_DESIGN.md` + `docs/VISUAL_COMPONENT_AND_STATE_SPECIFICATION.md`
  (Tabler 1.5.x patterns, mobile-first; confirmation dialogs follow the
  existing destructive-action pattern).

---

## 5. Security considerations

- Dump/archive files never transit Control Server disk; only metadata and
  streamed chunks.
- Secrets flow runtime-ward exactly as they already do for `db.command`
  (encrypted at rest, decrypted per-command, never logged).
- Restore extraction validates every archive entry against containment
  (traversal, absolute paths, symlink escapes) on the runtime — independent
  of the Control Server.
- `confirm_name` gate for overwrite-restore is enforced server-side.
- Audit events: `backup.created`, `backup.restored`, `backup.deleted`,
  `schedule.changed`, plus service actions now attributed
  `runtime`+`unit`.
- Log hygiene: dump stderr messages are recorded only on failure and are
  tool-authored connection errors, not data; chunk payloads are never logged.
- SQLite path: the connection's `database` field is a file path — the runtime
  resolves it inside the backups/dump flow with the same trust model as the
  existing SQLite connector (existing containment applies where a root is
  defined; a SQLite connection to an arbitrary path is already possible today
  and unchanged).

---

## 6. Proposed ADRs (finalized in slice commits)

- **ADR-041 — Backup files live on the runtime host.** Fixed root
  `~/.local/share/devstationone/backups/`; Control Server keeps metadata rows
  only and proxies chunked downloads. Rationale: dumps run where the dump
  tools and data are; keeps multi-GB files off the internet-facing box.
- **ADR-042 — Asynchronous backup jobs over `backup.command`/`backup.event`.**
  Long dumps can't ride the 15–30 s synchronous RPC timeouts; jobs report via
  pushed events with status polling as the reconnect fallback.
- **ADR-043 — Runtime-level services replace project-scoped services.**
  Services belong to the runtime host; project routes and UI tab removed
  without a compatibility shim.

---

## 7. Suggested slices (implementation order)

1. **Slice A — Runtime backup capability + DB backups (manual)**: protocol
   types, `backupManager` (pg/mariadb/mssql/sqlite dump + list + chunked
   read), `internal/backups` metadata + API, download proxy, Databases UI
   panel. Smoke: pg round-trip on dev instance.
2. **Slice B — DB restore + schedules**: restore action (existing/new DB,
   confirm gate), scheduler goroutine, schedule CRUD + UI, retention
   enforcement.
3. **Slice C — Project backups/restore**: tar.gz+manifest archive/extract,
   new-project and overwrite-restore (with session close + safety snapshot),
   Backups tab UI, project schedules reuse Slice B machinery.
4. **Slice D — Services to runtimes**: runtime-scoped routes + Runtimes
   Services view, delete project services tab/routes/UI, docs/OpenAPI
   cleanup, audit attribution change.