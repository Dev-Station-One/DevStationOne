---
id: account
title: Account
summary: Your profile, display name, fonts, Code tab languages, theme, and active login sessions.
---
# Account

Shows your email and role, and lets you set a **display name** — shown
instead of your email in places like the Journal, so co-workers see a
readable name rather than an address.

## Fonts

Default monospace font and size for the Terminal and the project
[Code](/help.html#code) editor, with a live preview. These are account-wide
defaults, not per-project.

## Code tab languages

The Code editor auto-detects a file's language from its name using
CodeMirror's full ~180-language table. This checklist controls which of
those are auto-detected for you — `JavaScript, TypeScript, Go, Python,
HTML, CSS, JSON, YAML, Markdown, SQL, Shell` are enabled by default.
Unchecking a language doesn't remove it: any file can still be set to it
manually from the tab's language picker, unchecking just stops it from
being guessed automatically (handy if two languages would otherwise fight
over the same file extension).

## Sessions

Every device/browser you're logged in from appears as a session. Revoke a
session to sign that device out immediately — useful if you left yourself
logged in somewhere, or suspect a session was stolen.

Theme (light/dark) is also an account-level preference, changed from the
sidebar's theme toggle, so it follows you across devices instead of being
stored per-browser.
