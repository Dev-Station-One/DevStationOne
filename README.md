# DevStation

DevStation is a self-hosted remote development environment platform.

**DevStation Control Server** is an Internet-facing Go application with PostgreSQL, an OpenAPI-first REST API, WebSocket terminal transport, authentication/authorization, and a responsive Tabler web UI.

**DevStation Runtime** is a separate Go application installed on a development machine. It runs as the normal development user, establishes an outbound secure connection to the Control Server, and exposes projects, persistent PTYs, filesystem operations, user-level systemd services, and database connectivity.

The MVP client is the browser.

## Core product model

```text
User
 └── Control Server
      ├── Runtimes
      │    ├── Projects
      │    │    ├── Session / PTY
      │    │    ├── Session / PTY
      │    │    ├── Files
      │    │    ├── Databases
      │    │    └── Services
      │    └── Capabilities
      └── API + Web UI
```

A Project is a first-class execution workspace. Multiple persistent terminal sessions may run simultaneously in one Project. Every Project session starts in the configured Project working directory, making tools such as Claude Code work naturally with project-local `CLAUDE.md`, `.claude/skills`, MCP configuration, source code, and other files.

## Design principles

- Self-hosted.
- Privacy by architecture.
- No vendor-hosted relay required.
- Development machines need no Internet-facing inbound port.
- Control Server and Development Runtime are separate applications.
- Go is the implementation language.
- PostgreSQL is the Control Server datastore.
- Do not use PostgREST.
- OpenAPI is a first-class contract.
- Browser is the only MVP client.
- Future Android/Linux/Windows clients use the same API.
- WebSocket is used for live terminal bytes; PostgreSQL is not a terminal-byte transport.
- Projects own execution context.
- Sessions are real PTYs and survive browser disconnects.
- Services are separate from interactive sessions and use `systemd --user`.
- Files and databases are first-class runtime capabilities.
- No stubs, fake implementations, TODO production paths, or placeholder endpoints.

## UI

Use the Tabler folded layout as the visual foundation:

https://preview.tabler.io/layout-folded.html

The application shell should have:
- top navigation
- foldable left navigation
- project navigation
- runtime/server navigation
- Files
- Databases
- Services
- Activity/Audit
- Settings

The Project workspace uses a tabbed interface inspired by terminal applications such as Ghostty.

## Initial build order

See `docs/IMPLEMENTATION_PLAN.md`.

The implementation is deliberately divided into complete vertical slices. Claude Code must finish and verify one slice before moving to the next.
