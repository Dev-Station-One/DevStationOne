---
id: docker
title: Docker
summary: Manage this project's Docker containers and compose stack, with live container logs.
---
# Docker

Manages the Docker containers that belong to this project on the runtime
machine — the containers of the project's compose stack, plus any container
that bind-mounts the project's working directory. Containers with no
connection to the project (other dev infrastructure on the same machine) are
never shown.

**Compose up** builds and starts the stack from the project's compose file
(`compose.yaml` / `docker-compose.yml` and variants). **Compose down** stops
and removes it. Output appears when the command finishes — pulls and builds
can take minutes. Compose needs the `docker compose` CLI plugin installed on
the runtime machine.

Each container row exposes Start, Stop, Restart, Remove, and **Logs**.
Logs stream live (`docker logs -f`), including stopped containers' retained
log; the stream stops when you close the pane or leave the tab.

**Images, volumes, networks** is a read-only view of the resources the
project's containers reference — Docker resources are never created or
deleted from here.

If the tab says Docker is not available, the runtime machine has no
reachable Docker daemon socket. Shell access to a container: use the
Terminal tab and run `docker exec -it <name> bash`.