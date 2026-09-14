# Backups

## What needs backing up

**PostgreSQL is the only stateful store the Control Server owns.** Every
persisted resource — users, sessions, runtimes, projects, terminal session
metadata, encrypted database connection credentials, audit events — lives
in the one `DEVSTATIONONE_DATABASE_URL` database (docs/ARCHITECTURE.md
"Persistence"). Back that database up; there is nothing else on the
Control Server to snapshot.

**`DEVSTATIONONE_DB_ENCRYPTION_KEY` must be backed up separately, alongside
the database, not instead of it.** It decrypts every stored database
connection's credentials (ADR-021). A database backup restored under a
*different* key is not useless — users, projects, sessions, and audit
history are all fine — but every stored database connection's password is
permanently unrecoverable, and each one has to be re-entered by hand.
Store the key wherever the deployment already keeps other secrets (a
password manager, a sealed secret in the orchestrator, etc.) — never in the
same place as the database backup itself, and never in git.

**Runtime-side files are not backed up by anything here.** A Project's
working directory lives on that Development Runtime's own filesystem —
whatever backup story that machine already has (its own disk snapshots,
the developer's own dotfiles/git repos) is what protects it. DevStationOne
has no opinion on that and doesn't touch it.

## Backing up PostgreSQL

Standard `pg_dump`/`pg_restore` — nothing DevStationOne-specific:

```bash
pg_dump "$DEVSTATIONONE_DATABASE_URL" -Fc -f devstationone-$(date +%F).dump
```

Restore into an empty database:

```bash
pg_restore -d "$DEVSTATIONONE_DATABASE_URL" devstationone-2026-01-01.dump
```

Take the backup with the Control Server stopped, or accept that it reflects
whatever was committed at the moment `pg_dump` ran (Postgres' own MVCC
snapshot makes this safe against corruption either way — it just means the
last few seconds of activity might not be included).

## Restore procedure

1. Stop the Control Server (and every connected Development Runtime, or at
   least accept they'll see a stale/gone Project until reconnected).
2. Restore the PostgreSQL dump into a fresh database.
3. Confirm `DEVSTATIONONE_DB_ENCRYPTION_KEY` is the *same key* that was in
   use when the backup was taken.
4. Start the Control Server — it runs any migrations newer than the
   backup automatically (`database.Migrate`).
5. Log in, spot-check a Project and a database connection (its stored
   credentials should decrypt and "Test connection" should succeed — that
   is the concrete proof the encryption key matches).

## Retention of audit events specifically

Audit events (`audit_events` table, Slice 11) have their own retention
policy independent of database backups — see the Activity page or
`GET /api/v1/audit/settings`. That setting controls when
DevStationOne itself deletes old audit rows (a daily background job,
`internal/audit.Purge`); it has no bearing on how far back a *database*
backup can restore. If audit history needs to outlive its retention
setting, keep it long enough, or rely on point-in-time database backups
taken before the policy would have purged them.
