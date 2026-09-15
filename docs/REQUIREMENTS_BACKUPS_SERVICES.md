# Requirements: Backups and Runtime-Level Services

Status: Requirements discovery complete (2026-09-15). No architecture or design
decisions are made here; see ADRs and implementation slices for those.

Three feature areas were explored:

1. Database backup/restore
2. Project backup/restore
3. Runtime-level service management (replacing project-scoped services)

---

## 1. Database backup/restore

### Goal

Users can back up any database accessible through a stored database connection
and restore any stored backup, without leaving the DevStationOne UI.

### Storage and trigger decisions

- Backup files are stored on the **runtime host disk**, next to the data they
  protect. The Control Server stores only metadata (Postgres rows) and proxies
  downloads. No large-file storage on the internet-facing Control Server.
- Both **manual backups and scheduled backups** are in scope. A scheduler in
  the Control Server fires per-database schedules and issues the backup RPC to
  the runtime.
- All four MVP engines are in scope: **PostgreSQL, MySQL/MariaDB, SQL Server,
  SQLite**. SQLite backup is a file copy / `.backup` operation; the others use
  their native dump tooling on the runtime host.

### Functional requirements

- FR-DB-1: Start a manual backup of a database (or named SQLite file) from the
  Databases UI. Backup runs on the runtime host.
- FR-DB-2: Create, edit, delete per-database schedules (cadence + retention
  count). Schedules run only while the runtime is connected; missed runs while
  offline are skipped (not queued) — acceptable, documented behavior.
- FR-DB-3: List backups per database with size, created-at, trigger
  (manual/scheduled), and download action.
- FR-DB-4: Download any backup through the Control Server (streamed proxy).
- FR-DB-5: Delete a backup file and its metadata row together.
- FR-DB-6: Restore a backup into **any database on the same connection**,
  including the original. Target may be an existing database (destructive —
  requires explicit typed confirmation) or a new database name.
- FR-DB-7: Backup progress/status surfaced in the UI (running, success,
  failure with error message). Failures never delete prior good backups.
- FR-DB-8: Retention: each schedule keeps the newest N backups for its
  database (user-set, default 10). Manual backups are never auto-deleted.
- FR-DB-9: Audit log entries for backup start, restore, delete, and schedule
  changes.

### Non-functional requirements

- NFR-DB-1: Terminal/database contents and credentials are never logged; dump
  command output that could contain data is not recorded.
- NFR-DB-2: Large dumps must stream (no full in-memory buffering of a backup
  in either server process).
- NFR-DB-3: Restore into an existing database is gated server-side, not just
  in the UI.

### Acceptance criteria

- Manual pg backup of a seeded database, download, restore into a second
  database — row counts match.
- Schedule fires while runtime connected; oldest file beyond N is removed.
- Restore to existing database requires confirmation; canceling leaves data
  untouched.
- SQLite backup restores by file copy and opens correctly.
- MySQL and SQL Server equivalents complete round-trip for a small test DB.

---

## 2. Project backup/restore

### Goal

Users can back up an entire Project (working directory contents plus project
metadata) and restore it — either as a brand-new Project or over an existing
one.

### Contents and format decisions

- A project backup contains **every file in the working directory** —
  `.gitignore` is **not** honored; nothing in the directory is excluded
  (backup, not snapshot-for-git) — **plus** project metadata (name,
  description, shell, environment variables). Database dumps are **not**
  included; database backup is its own feature.
### Decisions

- Format is a **tar.gz payload plus a sidecar JSON manifest** (name,
  created-at, format version, project metadata, file count, source). The
  runtime validates the manifest before restore.
- Backup files live under a **fixed root on the runtime host**
  (`~/.local/share/devstationone/backups/` with `db/` and `projects/`
  subtrees), the same storage area as database backups, with the same
  list/download/delete UX.
- Restore-as-new-project derives the working directory from the runtime root
  plus the sanitized project name, collision-suffixed (`-2`, `-3`); the user
  does not pick a directory.
- During overwrite-restore, all open sessions of that project are **closed
  first** (clean PTY shutdown); the user reopens them afterward.
- Schedules display **last run time, result, and next run** alongside their
  cadence and retention config.

### Functional requirements

- FR-PJ-1: Start a manual project backup from the Project UI.
- FR-PJ-2: Scheduled project backups with the same cadence + retention-N
  model as database schedules.
- FR-PJ-3: List/download/delete project backups per project.
- FR-PJ-4: Restore a backup as a **new project**: creates the Project record
  (metadata applied) and extracts the archive into a new working directory on
  the chosen runtime.
- FR-PJ-5: Restore over an **existing project**: runtime first captures an
  automatic safety snapshot of the current directory (same archive format,
  visible in the backup list), then wipes the directory contents and extracts.
  Destructive; explicit confirmation. The snapshot makes a single-step undo
  possible.
- FR-PJ-6: Environment variables inside project metadata are handled per the
  existing security rules (never logged; sensitive values not echoed back in
  plaintext beyond what the existing project editor already does).
- FR-PJ-7: Audit log entries for backup start, restore (new/overwrite), and
  delete.
- FR-PJ-8: All open sessions of the project are closed (clean PTY shutdown)
  before overwrite-restore extracts; the user reopens sessions afterward.

### Non-functional requirements

- NFR-PJ-1: Extraction performs containment checks identical to the existing
  filesystem package (no path traversal, no symlink escapes) for every entry
  in the archive.
- NFR-PJ-2: Streaming archive creation/extraction; bounded memory.

### Acceptance criteria

- Backup a project containing gitignored files — they are present in the
  archive.
- Restore-as-new-project produces a working project with matching metadata and
  opens sessions in the new directory.
- Overwrite-restore produces a pre-restore snapshot entry and leaves the
  directory containing exactly the archive contents afterward.
- A maliciously crafted archive with `../../` entries is rejected (tests).
- Restoring a project from runtime A onto runtime B works when the target
  runtime is online.

---

## 3. Runtime-level service management

### Goal

Service management moves from Project scope to Runtime scope. The runtime host
owns its services; the project Services tab goes away entirely.

### Decisions

- Services are managed at the **runtime level**: the Runtimes area gets a
  Services view (page or tab — design decision).
- The unit listing shows **all `systemctl --user` units** on the runtime host,
  with no project association or filtering.
- The project-scoped Services tab and its project-scoped API routes are
  **removed**. There is no compatibility shim; this is an internal tool with a
  small user base and the runtime is the correct owner of the concept.

### Functional requirements

- FR-SV-1: New runtime-scoped endpoints replace the project-scoped ones:
  list units, get status, start, stop, restart, enable, disable, fetch logs
  (same actions as today, keyed by runtime ID + unit instead of project ID +
  unit).
- FR-SV-2: Unit name validation and authorization identical in rigor to the
  current implementation (validate `{unit}`, authorize the runtime).
- FR-SV-3: UI: Services view on the Runtime detail area, list with status,
  actions, and the existing log viewer.
- FR-SV-4: Project workspace Services tab and its sidebar entry are removed;
  no dead links or dead routes remain.
- FR-SV-5: Audit log entries keep recording service actions, now attributing
  runtime + unit instead of project + unit.
- FR-SV-6: Protocol message (`service.command`) payload semantics unchanged
  except that resolution is by runtime connection directly; the payload may
  drop project context if the protocol doc is updated accordingly.

### Non-functional requirements

- NFR-SV-1: No new privilege surface: still `systemctl --user` as the runtime
  user; never root.
- NFR-SV-2: OpenAPI, PROTOCOL.md, and the UI docs updated in the same slice.

### Acceptance criteria

- Runtimes page lists all user units for a connected runtime; start/stop/
  restart/enable/disable/logs all work against a real test unit.
- Project workspace no longer shows a Services tab; old project service
  endpoints return 404/405 (documented removal).
- Runtime offline shows the existing offline conflict state, not a hang.
- A unit name containing `/`, spaces, or control characters is rejected.

---

## Resolved questions (all answered 2026-09-15; decisions inline above)

1. Archive format: tar.gz + sidecar JSON manifest (FR-PJ format section).
2. Schedule status display: yes — last run, result, next run.
3. Backup storage location: fixed root on runtime host,
   `~/.local/share/devstationone/backups/` with `db/` and `projects/`
   subtrees; free-disk constraints not surfaced (out of scope this round).
4. Restore-as-new-project target directory: derived from runtime root +
   sanitized project name, collision-suffixed.
5. Open terminal sessions during overwrite-restore: closed first (clean PTY
   shutdown); FR-PJ-8 resolved accordingly.