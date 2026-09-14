# Project Journal — Requirements

Status: requirements agreed 2026-09-14 (brainstorm). Architecture and API design follow in a separate pass.

## Goal

Developers keep timestamped, rich-text notes attached to a Project —
work log, decisions, findings — optionally linked to a git commit.

## Where it lives

New **Journal** tab in the Project workspace, alongside
Terminal/Files/Databases/Services. Layout follows the Tabler
datatables pattern (https://preview.tabler.io/tables.html) already used
for the Files table.

## Functional requirements

### Note record

- Belongs to exactly one Project.
- Fields: rich-text HTML body (Summernote), created_by, created_at,
  updated_at, optional commit association (SHA + subject + commit
  datetime + commit author, captured at link time).
- Creator name = `users.display_name` (new nullable column), falling
  back to email when unset. Requires a display-name preference field in
  the profile/preferences UI.
- Anyone with project access can create, edit, and delete any note.
- Edits overwrite the body and update `updated_at`; the table and detail
  view show an "Edited <date>" marker when `updated_at != created_at`.

### Attachments

- Stored in the Control Server (Postgres), not the runtime.
- Uploaded via the note editor; pasted images in Summernote become
  attachment rows (never inline base64).
- Limits: 25 MB per attachment, 10 attachments per note; over-limit
  rejected with a clear error.
- Downloadable by anyone with project access; deleted with the note.

### Commit association

- Optional. The note editor offers a commit picker populated from the
  project's git history via the runtime (new git RPC `log` action on top
  of the existing git.command pipeline).
- Picker rows show: commit id | message | commit datetime | commit
  author (submitter).
- Free-text SHA entry is also allowed (for commits outside the recent
  list).
- Association is recorded as plain metadata (SHA, subject, date,
  author) at link time — no live GitHub API dependency.

### Journal table

Columns, in order:

| DateTime | Snippet | Created By | Actions |
|---|---|---|---|
| created_at, "Mon Sep 14, 11:11 AM" format | first ~120 chars of HTML-stripped body text | display_name (email fallback) | edit, delete icons |

- Server-side pagination (limit/offset + total count) — same pattern as
  the audit log API.
- Delete requires confirmation.

### Note editor

- Summernote (Bootstrap 5.3 build) in an XL modal: toolbar, image paste
  and upload, attachment list.
- View: clicking a row opens an XL modal with the fully rendered note,
  attachment list with download links, and commit link.

## Non-functional requirements

- **XSS**: stored note HTML is untrusted — sanitize server-side on
  write (allowlist of Summernote-safe tags/attributes) before storage or
  render. Attachment downloads get safe Content-Disposition handling
  (existing files API pattern).
- Timestamps render in the user's local timezone, "Mon Sep 14, 11:11 AM"
  format (Intl-based, matching existing UI date formatting).
- No terminal I/O, secrets, or file contents logged; journal body and
  attachments never logged.
- Journal events (create/edit/delete/attach) recorded in the audit log
  like other project mutations.

## User stories / acceptance criteria

1. As a developer, I open the Journal tab, click New, write rich text,
   paste a screenshot, and save — the row appears at the top with my
   display name and correct timestamp.
2. I link a note to a commit via the picker; the row/detail shows the
   association (SHA, message, date, author).
3. I click edit, change the body, save — the row shows "Edited" with
   the new timestamp.
4. I paste a 30 MB file — rejected with a clear size error, note not
   saved half-broken.
5. Another user with project access edits my note — allowed; creator
   name in the row stays the original author.
6. I delete a note — confirmation prompt, then row disappears and
   attachments are gone.
7. Pagination: with more notes than one page, paging fetches the next
   server-side page without loading all notes.

## Open items (non-blocking, resolve at design pass)

- Summernote vendored vs CDN — resolved: vendored into
  `web/static/vendor/summernote` (BS5 build on the existing vendored
  jQuery; Bootstrap 5.3 JS also vendored for its tooltip dependency).
- Whether audit detail stores a body snippet — resolved: no, snippet is
  display-only; audit records event metadata only.

## Implemented deviations

- Sanitization: render-time only, via the already-vendored DOMPurify at
  every render point (row snippet is HTML-stripped server-side; full
  body is sanitized in the viewer before it touches the DOM). No new Go
  sanitizer dependency; untrusted HTML never reaches the DOM
  unsanitized. The original requirement said "sanitize server-side on
  write" — superseded by this documented deviation.
- Attachment removal: notes delete cascades to attachments (no
  per-attachment delete endpoint); the editor's remove action applies
  only to not-yet-saved uploads. Add a delete endpoint if needed.
- Commit picker data comes from the runtime's `git log` (new `log`
  action), not GitHub's API; the stored association is the captured
  metadata.