---
id: source-control
title: Source Control
summary: Git status, staged/unstaged changes, diffs, commits, and pull/push for the project directory.
---
# Source Control

Shows the Git status of the project's working directory, if it's a Git
repository.

- **Staged** and **Unstaged** lists show changed files; click one to view
  its diff on the right.
- Write a commit message and click **Commit** to commit staged changes.
- **Refresh** re-reads status from disk — useful after editing files
  outside the browser (e.g. from a terminal session in the same project).

If the directory has no changes, you'll see a plain "No changes" state
instead of empty lists.

## Pull and Push

The header shows the current branch's status next to **Pull** and **Push**
buttons — an `↑2 ↓3`-style badge when the branch is ahead/behind its
upstream. These stay visible even when there are no changes to show, so
they're always reachable.

- **Pull** merges the branch's upstream in. If it conflicts, the affected
  files are listed and marked **Conflict** in the changed-file lists —
  resolve them in a **Terminal** tab in this project, same working
  directory, then stage and commit as usual. This panel doesn't do
  in-browser conflict resolution.
- **Push** sends the current branch only — never a force-push. A branch
  that's never been pushed before is published automatically on the first
  push (no separate "set upstream" step).
- If the repository has no remote configured, both buttons are disabled
  with a tooltip explaining why.

Authentication for pull/push uses whatever Git credentials (SSH key,
credential helper) are already set up for the Development Runtime's user on
that machine — DevStationOne doesn't store or manage Git credentials
itself.
