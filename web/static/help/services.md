---
id: services
title: Services
summary: Manage systemd --user services for this project's dev servers and background processes.
---
# Services

Lists `systemd --user` services configured for this project — dev servers,
watchers, workers, anything you'd normally manage with `systemctl --user`.

Each service card exposes Start, Stop, Restart, Enable (start on boot/login),
Disable, and Logs — the same actions `systemctl --user` gives you, without
needing a terminal open.

None of this requires root: the runtime manages these entirely as your
normal user's systemd instance. **Refresh** re-reads current status from the
runtime.
