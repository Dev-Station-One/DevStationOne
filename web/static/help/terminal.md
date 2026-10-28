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
- **Dictate to terminal** (the mic button in the toolbar) turns on
  speech-to-text: spoken text is pasted into the session like typed keys,
  so you can speak a command instead of typing it. Dictation uses the Web
  Speech API — currently Chrome and Edge — and asks for microphone
  permission on first use; the mic button pulses while it is listening.
  Your browser's speech service processes the audio (nothing is logged by
  DevStationOne), and nothing is sent to the project's runtime beyond the
  recognized text. Dictation stops when the session changes and turns
  itself off if the session loses its connection.
- **Force stop** kills the underlying process if a session becomes
  unresponsive.

Terminal input and output are never written to the audit log.
