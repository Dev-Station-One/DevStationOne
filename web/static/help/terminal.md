---
id: terminal
title: Terminal
summary: Real PTY sessions in your project's working directory, shell, and OS user.
---
# Terminal

Each terminal tab is a real PTY (not a simulated console), started in the
project's working directory, running as the project's OS user, with the
project's chosen shell. Claude Code, `bash`, or any other CLI tool runs here
exactly as it would over a normal SSH session.

- **+ New** opens another session; a project can have several running at
  once.
- Sessions survive a browser disconnect — close the tab or lose your
  connection, and reopening the project reattaches you to the same session
  instead of starting a new one.
- The key-send buttons (Ctrl+C, Ctrl+D, Tab, Esc) send real signals/keys —
  useful on mobile keyboards that don't have them.
- **Force stop** kills the underlying process if a session becomes
  unresponsive.

Terminal input and output are never written to the audit log.
