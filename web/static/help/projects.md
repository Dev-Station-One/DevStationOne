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

1. Pick a registered, online [Runtime](/help.html#runtimes) — offline
   runtimes can't be selected for the shell/working-directory fields.
2. Pick a shell reported by that runtime.
3. Browse to (or type) a working directory. This becomes the project's home
   for every terminal session, file operation, and service opened inside it.
4. Optionally set environment variables, one `KEY=VALUE` per line.

## Managing projects

Click a project row to open its workspace (Terminal, Files, Code, Source
Control, Databases, Services, Journal). Use the trash icon on a row to
delete a project — this does not touch files on disk, only the project
record and its sessions.

The working directory you set here is authoritative: once a project exists,
nothing sent from the browser can redirect a session to a different
directory.
