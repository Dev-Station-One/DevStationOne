---
id: project-overview
title: Project workspace
summary: The project workspace bundles Terminal, Commands, Files, Code, Source Control, Databases, Backups, Docker, and Journal for one project.
---
# Project workspace

Every project opens into the same set of tabs, all scoped to that project's
runtime, OS user, and working directory:

- [Terminal](/help.html#terminal) — real PTY sessions.
- [Commands](/help.html#commands) — named CLI commands you can run with one click, with run history.
- [Files](/help.html#files) — browse, edit, upload/download.
- **Code** — a multi-file [code editor](/help.html#code).
- [Source Control](/help.html#source-control) — Git status, diff, stage/commit, and pull/push.
- [Databases](/help.html#databases) — connect to and browse project databases.
- [Backups](/help.html#backups) — back up and restore this project's working directory.
- [Docker](/help.html#docker) — manage this project's containers and Compose stack.
- [Journal](/help.html#journal) — timestamped notes about this project.

The **Overview** tab you're likely looking at now shows the project's name,
runtime, OS user, and working directory at a glance. Use the pencil icon to
edit the project's settings, or the trash icon to delete it (this removes
the project record only, not files on disk).

[Services](/help.html#services) (`systemd --user`) aren't a project tab —
they belong to the runtime host, not any one project, so they're managed
from the **Services** button next to that runtime on the **Runtimes** page.

The help icon next to the tab bar always describes whichever tab is
currently open.
