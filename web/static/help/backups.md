---
id: backups
title: Backups
summary: Back up and restore database connections and project directories.
---
# Backups

Backup files are created on the runtime host by that engine's own tooling
(`pg_dump`, `mysqldump`, `sqlcmd BACKUP DATABASE`, SQLite's `.backup`) or, for
a project, a full archive of its working directory — nothing is excluded,
`.gitignore` is not honored. Files never pass through or land on the Control
Server's disk; downloads stream through it in chunks.

## Database backups

Found alongside each connection in the **Databases** tab. **Restore** asks
for a target database name — it can be the same connection's original
database or a new one; if the target already exists, its data is replaced,
so you're asked to confirm before it runs.

## Project backups

Found in this project's own **Backups** tab. A backup can be restored two
ways:

- **Restore as new project** — extracts into a fresh working directory and
  creates a new Project record. Never touches the original.
- **Restore over this project** — destructive. Requires typing the project's
  name to confirm. Any open terminal sessions are closed first, then a
  safety snapshot of the current directory is taken automatically (so this
  can be undone once), and finally the directory is replaced with the
  backup's contents.

## Schedules

Each connection or project can have one schedule: a cadence (hourly, daily,
weekly) and how many scheduled backups to keep — older ones are deleted
automatically as new ones complete. Manual backups are never auto-deleted.
If the runtime is offline when a schedule fires, that run is skipped rather
than queued.
