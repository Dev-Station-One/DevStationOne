---
id: account
title: Account
summary: Your profile, display name, fonts, Code tab languages, theme, active login sessions, and two-factor authentication.
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

## Two-factor authentication

Every account must have a second sign-in factor. The first time you sign in
with your password, you're walked through setting one up before you can
reach the rest of the app — there's nothing to turn on separately.

Choose one of:

- **Authenticator app** — enter the shown setup key into an app like Google
  Authenticator, Authy, or 1Password, then enter the 6-digit code it
  generates.
- **Passkey** — use your device's built-in biometric/PIN unlock or a
  security key. A passkey can also sign you in on its own afterward, with
  no password step at all ("Sign in with a passkey" on the login page).
- **Email codes** — a one-time code is sent to your account email each time
  you sign in.

Right after you confirm your first method, you get 10 **backup codes** —
save them somewhere safe. Each works once, in place of your normal second
factor, if you don't have access to your usual method.

On the sign-in prompt, checking **"Trust this device for 30 days"** skips
the second factor on that browser until it expires or you're signed out;
your password is still always required. Lost your second factor entirely?
Use a backup code, or ask an administrator to reset your two-factor setup —
either puts you straight back through the setup above on your next sign-in.
