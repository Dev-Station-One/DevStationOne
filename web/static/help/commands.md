---
id: commands
title: Quick Commands
summary: Store per-project CLI commands (Build, Test, clean up) and run one with a click — each opens a real terminal tab; history keeps the result and the last output.
---

## Quick Commands

The **Commands** tab holds a project's named CLI commands. One click runs
one in a fresh terminal (a real PTY in the project's working directory via
its shell), named after the command — "Build", "Test", "Clean up" — and
behaves like any terminal session: you can watch, interact, and close it.

### Add or edit

- **New command**: a name (button label, e.g. "Build") and the command
  string (e.g. `make build`). Both required.
- Pencil edits, trash deletes (history of the command is kept even after
  deletion).

### Import

**Import** scans the project for known command sources and lists what it
found with a checkbox next to each: `Makefile` targets, `package.json`
`scripts` (npm), and executable files in `scripts/`. Tick the ones you
want, press **Import selected**, and edit names/commands before saving.
Missing files are normal — the scan simply offers nothing for that source.

### Run and history

Clicking **Run** opens a new terminal tab running the command (multiple
simultaneous runs of the same command are fine). Every run is recorded —
who ran it, when, how long, the exit code — with the last 64 KiB of output
captured at exit (ADR-045). Expand **History** under a command to see its
last runs and open the captured output of any finished one.