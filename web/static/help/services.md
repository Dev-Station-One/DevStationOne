---
id: services
title: Services
summary: Manage systemd --user services on a runtime host.
---
# Services

Lists `systemd --user` services on the selected runtime's host — dev
servers, watchers, workers, anything you'd normally manage with
`systemctl --user`. Services belong to the runtime host, not to any single
project, so they're managed from the **Runtimes** page.

Each service card exposes Start, Stop, Restart, Enable (start on boot/login),
Disable, and Logs — the same actions `systemctl --user` gives you, without
needing a terminal open.

None of this requires root: the runtime manages these entirely as your
normal user's systemd instance. **Refresh** re-reads current status from the
runtime.
