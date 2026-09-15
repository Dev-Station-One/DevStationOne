---
id: projects
title: Projects
summary: Create and manage the projects that define where and how you work.
---
# Projects

A **Project** defines an execution context: which runtime it runs on, which
OS user and shell it uses, its working directory, and its environment
variables.

## Creating a project

Click **New project** (top right) — the form opens in a modal:

1. Pick a registered, online [Runtime](/help.html#runtimes) — offline
   runtimes can't be selected for the shell/working-directory fields.
2. Pick a shell reported by that runtime.
3. Browse to (or type) a working directory. This becomes the project's home
   for every terminal session, file operation, and service opened inside it.
4. Optionally give the project a description — it appears under the name in
   the list and is matched by the search box.
5. Optionally set environment variables, one `KEY=VALUE` per line.

## Finding projects

The projects list shows every project on the server. Use the search box to
match on name, description, or working directory, the owner and runtime
dropdowns to narrow the list, and click a column header to sort. "Last
accessed" shows when a terminal session was last opened on the project.

## Managing projects

Click a project row to open its workspace (Terminal, Files, Code, Source
Control, Databases, Services, Journal). Only the project's owner (or an
administrator) sees the edit and delete buttons — edit opens the same modal
used for creating, delete removes the project record and its sessions but
does not touch files on disk.

The working directory you set here is authoritative: once a project exists,
nothing sent from the browser can redirect a session to a different
directory.
