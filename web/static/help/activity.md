---
id: activity
title: Activity
summary: The audit log of security-relevant events, visible to administrators.
---
# Activity

Activity is the audit log: authentication, runtime registration/revocation,
project changes, session lifecycle, database administration, service
actions, and file operations are all recorded here with who did it, when,
from what IP, and what was affected.

Only administrators can view this page — everyone else is redirected.

## Filters and retention

Filter by event type and a time range with **Apply**. Use **Newer**/**Older**
to page through results.

**Retention policy** controls how many days audit events are kept before
they're purged; set it to `0` to keep events forever.

Sensitive values (passwords, tokens, file contents, terminal input/output)
are never written to this log — only metadata about what happened.
