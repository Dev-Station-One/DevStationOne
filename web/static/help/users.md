---
id: users
title: Users
summary: Manage user accounts, roles, and access (administrators only).
---
# Users

The Users page lists every account on this Control Server. Only
administrators can see it and the `Users` sidebar entry; the API enforces
the same gate server-side.

**Last accessed** is when the account was last authenticated — any signed-in
request refreshes it.

## Creating a user

Click **New user**: set an email, an optional display name (shown in Owner
columns across the app instead of the email), a role, and a password of at
least 8 characters. Emails must be unique.

## Editing a user

The pencil icon opens the same modal, pre-filled. You can change the
display name, the role, or set a new password (leave the password field
blank to keep the existing one). Email is fixed after creation — an
account's identity.

## Guard rails

- You cannot demote or delete **your own** account.
- The **last remaining administrator** cannot be demoted or deleted.
- A user who still owns projects, runtimes, backup schedules, or database
  connections cannot be deleted — the delete error names what's in the
  way, and the **Owned resources** panel lets each project and runtime be
  reassigned to another user (or everything at once with "Reassign
  everything"). A runtime transfer only changes its owner; its enrollment
  and connection are untouched.
- Deleting a user removes their sign-in sessions immediately. Their past
  activity — terminal sessions, journal notes, quick-command runs,
  backups — survives, shown without a creator name; audit history is
  kept.

## Two-factor authentication

Every account, including ones created here, must set up a second sign-in
factor the first time it's used — there's no separate step for you to
enable it. See [Account](/help.html#account) "Two-factor authentication"
for what a user is walked through.