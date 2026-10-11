// Package protocol defines the versioned control-plane message envelope
// exchanged over the Control Server <-> Development Runtime WebSocket
// connection. Terminal/file/service/database payloads are added in the
// slices that implement them.
package protocol

import (
	"encoding/json"
	"time"
)

// Version is the current control-plane protocol version.
const Version = "1"

// MessageType identifies the kind of payload carried by an Envelope.
type MessageType string

const (
	// MessageHello is sent once by the runtime immediately after connecting,
	// advertising its identity and capabilities.
	MessageHello MessageType = "hello"
	// MessageHeartbeat is sent periodically by the runtime to prove liveness.
	MessageHeartbeat MessageType = "heartbeat"
	// MessageRevoked is sent by the server before closing a connection whose
	// runtime credential has been revoked.
	MessageRevoked MessageType = "revoked"

	// MessageSessionOpen is sent server->runtime to start a new PTY session.
	MessageSessionOpen MessageType = "session.open"
	// MessageSessionReady is sent runtime->server once a PTY session has
	// started successfully.
	MessageSessionReady MessageType = "session.ready"
	// MessageSessionError is sent runtime->server when a session fails to
	// start, or hits a runtime-side error.
	MessageSessionError MessageType = "session.error"
	// MessageSessionResize is sent server->runtime to change a PTY's
	// terminal dimensions.
	MessageSessionResize MessageType = "session.resize"
	// MessageSessionSignal is sent server->runtime to deliver a POSIX signal
	// to a session's process.
	MessageSessionSignal MessageType = "session.signal"
	// MessageSessionClose is sent server->runtime to terminate a session's
	// process and release its PTY.
	MessageSessionClose MessageType = "session.close"
	// MessageSessionClosed is sent runtime->server acknowledging a
	// session.close request.
	MessageSessionClosed MessageType = "session.closed"
	// MessageSessionExit is sent runtime->server when a session's process
	// exits on its own (not via session.close).
	MessageSessionExit MessageType = "session.exit"

	// MessageServiceCommand is sent server->runtime to list/inspect/control
	// a systemd --user service. Unlike session messages, it uses Envelope.ID
	// for request/response correlation (see runtimes.Conn.SendCommand): a
	// single runtime connection may have several service commands in flight
	// at once, with no other natural per-request identifier to key on.
	MessageServiceCommand MessageType = "service.command"
	// MessageServiceResult is sent runtime->server with a successful
	// service.command result, carrying the same Envelope.ID as the request.
	MessageServiceResult MessageType = "service.result"
	// MessageServiceError is sent runtime->server when a service.command
	// fails, carrying the same Envelope.ID as the request.
	MessageServiceError MessageType = "service.error"

	// MessageFileCommand is sent server->runtime to perform a filesystem
	// operation inside a Project's authorized root directory. Like
	// service.command, it uses Envelope.ID for request/response correlation.
	MessageFileCommand MessageType = "file.command"
	// MessageFileResult is sent runtime->server with a successful
	// file.command result, carrying the same Envelope.ID as the request.
	MessageFileResult MessageType = "file.result"
	// MessageFileError is sent runtime->server when a file.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageFileError MessageType = "file.error"

	// MessageDBCommand is sent server->runtime to perform a database
	// operation (Slice 7: connection test only so far). Like
	// service.command, it uses Envelope.ID for request/response correlation.
	MessageDBCommand MessageType = "db.command"
	// MessageDBResult is sent runtime->server with a successful db.command
	// result, carrying the same Envelope.ID as the request.
	MessageDBResult MessageType = "db.result"
	// MessageDBError is sent runtime->server when a db.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageDBError MessageType = "db.error"

	// MessageGitCommand is sent server->runtime to perform a git operation
	// inside a Project's authorized root directory. Like service.command, it
	// uses Envelope.ID for request/response correlation.
	MessageGitCommand MessageType = "git.command"
	// MessageGitResult is sent runtime->server with a successful git.command
	// result, carrying the same Envelope.ID as the request.
	MessageGitResult MessageType = "git.result"
	// MessageGitError is sent runtime->server when a git.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageGitError MessageType = "git.error"

	// MessageCodeCommand is sent server->runtime to perform an editor
	// operation (open/save/watch) against a watched file set in a Project
	// root. Like file.command, it uses Envelope.ID for request/response
	// correlation.
	MessageCodeCommand MessageType = "code.command"
	// MessageCodeResult is sent runtime->server with a successful
	// code.command result, carrying the same Envelope.ID as the request.
	MessageCodeResult MessageType = "code.result"
	// MessageCodeError is sent runtime->server when a code.command fails,
	// carrying the same Envelope.ID as the request. Its payload shape is
	// identical to ServiceErrorPayload, which is reused for it.
	MessageCodeError MessageType = "code.error"
	// MessageCodeFileChanged is pushed runtime->server (no Envelope.ID — it
	// is not a reply) when a watched file's mtime/size changed, or the file
	// vanished, as observed by the edit manager's poll loop.
	MessageCodeFileChanged MessageType = "code.file.changed"

	// MessageDockerCommand is sent server->runtime to manage the project's
	// Docker containers/compose stack (ADR-040). Like service.command, it
	// uses Envelope.ID for request/response correlation.
	MessageDockerCommand MessageType = "docker.command"
	// MessageDockerResult is sent runtime->server with a successful
	// docker.command result, carrying the same Envelope.ID as the request.
	MessageDockerResult MessageType = "docker.result"
	// MessageDockerError is sent runtime->server when a docker.command
	// fails, carrying the same Envelope.ID as the request. Its payload shape
	// is identical to ServiceErrorPayload, which is reused for it.
	MessageDockerError MessageType = "docker.error"
	// MessageDockerLogs is pushed runtime->server (no Envelope.ID — it is
	// not a reply) carrying one batch of followed container log lines,
	// keyed by the StreamID from the docker.command logs_start request.
	MessageDockerLogs MessageType = "docker.logs"

	// MessageBackupCommand is sent server->runtime to create, inspect,
	// download, or restore a backup (ADR-041: backup files live on the
	// runtime host). Like service.command, it uses Envelope.ID for
	// request/response correlation. "start" replies immediately with a job
	// ID and runs the dump/archive in the background; completion is learned
	// by polling "status" (no unsolicited backup event exists — ADR-042).
	MessageBackupCommand MessageType = "backup.command"
	// MessageBackupResult is sent runtime->server with a successful
	// backup.command result, carrying the same Envelope.ID as the request.
	MessageBackupResult MessageType = "backup.result"
	// MessageBackupError is sent runtime->server when a backup.command
	// fails, carrying the same Envelope.ID as the request. Its payload
	// shape is identical to ServiceErrorPayload, which is reused for it.
	MessageBackupError MessageType = "backup.error"
)

// Envelope is the outermost shape of every control-plane message. ID is an
// optional correlation identifier for request/response exchanges — used by
// service.command/service.result/service.error (Slice 5); session messages
// (Slice 4) correlate on their payload's own session_id instead, since that
// is already unique per request.
type Envelope struct {
	Version string          `json:"v"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Capabilities reports what the runtime can currently do. Fields stay false
// until the slice implementing that capability lands; advertising true
// without a real implementation would misrepresent the runtime.
type Capabilities struct {
	Terminal   bool     `json:"terminal"`
	Filesystem bool     `json:"filesystem"`
	Services   bool     `json:"services"`
	Databases  []string `json:"databases,omitempty"`
	// OSUsername and HomeDir identify the OS user the runtime process runs
	// as, replacing the old per-Project os_user field: a runtime always runs
	// as one fixed user (ADR-014), so the identity is reported here instead
	// of typed in by a client.
	OSUsername string `json:"os_username"`
	HomeDir    string `json:"home_dir"`
	// Shells is parsed from /etc/shells. Empty when unreadable — no
	// fallback list is offered, matching the Project setup UI's strict
	// dropdown (a client must not be able to type an arbitrary shell path).
	Shells []string `json:"shells,omitempty"`
	// Users and Groups are parsed from /etc/passwd and /etc/group (account/
	// group names only — no passwords, no other columns). They back the
	// file properties owner/group dropdowns. A non-root runtime (the only
	// supported mode, ADR-014) can only chown to a user it already owns the
	// file as, and can only chgrp to a group it belongs to — the dropdown
	// still lists every system account/group verbatim, same "report reality,
	// don't curate" rule as Shells; a chown/chgrp the OS rejects surfaces as
	// a normal file.error.
	Users  []string `json:"users,omitempty"`
	Groups []string `json:"groups,omitempty"`
	// Docker reports real, current availability of the local Docker daemon
	// (ADR-040): socket reachable, daemon version, and whether the compose
	// CLI plugin is installed. Nil when the runtime build has no docker
	// support or no socket could be reached.
	Docker *DockerCapability `json:"docker,omitempty"`
}

// DockerCapability describes the runtime host's Docker daemon availability.
type DockerCapability struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	// Compose is true when the `docker compose` CLI plugin is installed.
	Compose bool `json:"compose"`
}

// HelloPayload is the payload of a MessageHello envelope.
type HelloPayload struct {
	Hostname       string       `json:"hostname"`
	OS             string       `json:"os"`
	Arch           string       `json:"arch"`
	RuntimeVersion string       `json:"runtime_version"`
	Capabilities   Capabilities `json:"capabilities"`
}

// SessionOpenPayload is the payload of a MessageSessionOpen envelope. It
// carries everything the runtime needs to start a PTY without trusting any
// value the browser supplied directly: the Control Server resolves these
// from the Project record.
type SessionOpenPayload struct {
	SessionID string            `json:"session_id"`
	Shell     string            `json:"shell"`
	Dir       string            `json:"dir"`
	OSUser    string            `json:"os_user"`
	Env       map[string]string `json:"env,omitempty"`
	// Command optionally runs `shell -c <command>` instead of an
	// interactive shell prompt (Quick Commands, ADR-045). Empty = the plain
	// shell, exactly the pre-ADR-045 behavior.
	Command string `json:"command,omitempty"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

// SessionReadyPayload is the payload of a MessageSessionReady envelope.
type SessionReadyPayload struct {
	SessionID string `json:"session_id"`
}

// SessionErrorPayload is the payload of a MessageSessionError envelope.
type SessionErrorPayload struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
}

// SessionResizePayload is the payload of a MessageSessionResize envelope.
type SessionResizePayload struct {
	SessionID string `json:"session_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// SessionSignalPayload is the payload of a MessageSessionSignal envelope.
// Signal is a POSIX signal name such as "SIGINT" or "SIGTERM".
type SessionSignalPayload struct {
	SessionID string `json:"session_id"`
	Signal    string `json:"signal"`
}

// SessionClosePayload is the payload of a MessageSessionClose envelope.
type SessionClosePayload struct {
	SessionID string `json:"session_id"`
}

// SessionClosedPayload is the payload of a MessageSessionClosed envelope.
type SessionClosedPayload struct {
	SessionID string `json:"session_id"`
}

// SessionExitPayload is the payload of a MessageSessionExit envelope.
type SessionExitPayload struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
}

// ServiceCommandPayload is the payload of a MessageServiceCommand envelope.
type ServiceCommandPayload struct {
	// Action is one of: list, status, start, stop, restart, enable,
	// disable, logs.
	Action string `json:"action"`
	// Unit is the systemd unit name, e.g. "my-app.service". Required for
	// every action except "list".
	Unit string `json:"unit,omitempty"`
	// Lines is the tail length for a "logs" action.
	Lines int `json:"lines,omitempty"`
}

// ServiceInfo describes one systemd --user unit.
type ServiceInfo struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	// Enabled is "enabled", "disabled", "static", or "unknown" if the
	// runtime could not determine it.
	Enabled string `json:"enabled"`
}

// ServiceResultPayload is the payload of a MessageServiceResult envelope.
// Exactly one of Services, Service, or Logs is populated, matching the
// request's Action.
type ServiceResultPayload struct {
	Services []ServiceInfo `json:"services,omitempty"`
	Service  *ServiceInfo  `json:"service,omitempty"`
	Logs     []string      `json:"logs,omitempty"`
}

// ServiceErrorPayload is the payload of a MessageServiceError envelope.
type ServiceErrorPayload struct {
	Error string `json:"error"`
}

// DockerCommandPayload is the payload of a MessageDockerCommand envelope.
// Container and ComposeFile are always validated by the runtime against the
// project scope (ADR-040) — the client's value only ever selects among
// resources the scoped list already showed it.
type DockerCommandPayload struct {
	// Action is one of: list, start, stop, restart, remove, compose_up,
	// compose_down, compose_stop, compose_restart, logs_start, logs_stop,
	// resources.
	Action string `json:"action"`
	// Root is the Project's working directory, taken from the Project
	// record — never from a client request, same rule as
	// FileCommandPayload.Root. Compose-file detection, container scoping,
	// and compose subprocess cwd all resolve against it.
	Root string `json:"root"`
	// Container is the target container ID or name. Required for every
	// action except compose_* and logs_stop.
	Container string `json:"container,omitempty"`
	// RemoveVolumes accompanies a "remove" action's anonymous-volume cleanup.
	RemoveVolumes bool `json:"remove_volumes,omitempty"`
	// StreamID keys a logs_start/logs_stop pair; log batches are pushed as
	// MessageDockerLogs with the same StreamID.
	StreamID string `json:"stream_id,omitempty"`
	// Tail is the number of past lines a logs_start replays before
	// following.
	Tail int `json:"tail,omitempty"`
}

// ContainerInfo is the project-scoped projection of one Docker container.
type ContainerInfo struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Image  string   `json:"image"`
	State  string   `json:"state"`
	Status string   `json:"status"`
	Health string   `json:"health,omitempty"`
	Ports  []string `json:"ports,omitempty"`
	// ComposeService/ComposeProject are the com.docker.compose.* labels,
	// empty for non-compose containers that made the scope via bind mounts.
	ComposeService string `json:"compose_service,omitempty"`
	ComposeProject string `json:"compose_project,omitempty"`
}

// ImageInfo is a read-only image summary (ADR-040: images are never managed
// from the UI).
type ImageInfo struct {
	ID      string `json:"id"`
	Tags    []string
	Size    int64 `json:"size"`
	Created int64 `json:"created"`
}

// VolumeInfo is a read-only volume summary.
type VolumeInfo struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

// NetworkInfo is a read-only network summary.
type NetworkInfo struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

// DockerStatusPayload is the payload of a "list" docker.command result:
// everything the Docker tab's first render needs in one round trip.
type DockerStatusPayload struct {
	// Available is false when the runtime host has no reachable Docker
	// daemon; then Version/ComposeFile/Containers are empty.
	Available   bool   `json:"available"`
	Version     string `json:"version,omitempty"`
	Compose     bool   `json:"compose"`
	ComposeFile string `json:"compose_file,omitempty"`
	// Containers is always an array (never null/omitted) — the tab renders
	// from it directly.
	Containers []ContainerInfo `json:"containers"`
}

// DockerComposeOutputPayload is the payload of a compose_* docker.command
// result: the bounded tail of the compose CLI's combined output.
type DockerComposeOutputPayload struct {
	Output string `json:"output"`
}

// DockerResourcesPayload is the payload of a "resources" docker.command
// result: read-only images/volumes/networks referenced by the project's
// scoped containers.
type DockerResourcesPayload struct {
	Images   []ImageInfo   `json:"images,omitempty"`
	Volumes  []VolumeInfo  `json:"volumes,omitempty"`
	Networks []NetworkInfo `json:"networks,omitempty"`
}

// DockerLogsPayload is the payload of a MessageDockerLogs push: one batch of
// followed log lines for the stream keyed by StreamID. End is set on the
// final push before the stream closes normally (follow ended, container
// removed).
type DockerLogsPayload struct {
	StreamID string   `json:"stream_id"`
	Lines    []string `json:"lines"`
	End      bool     `json:"end,omitempty"`
}

// MaxFileBytes caps the size of one file transferred in a file.command
// read/write payload (base64-encoded inside the JSON envelope).
// ponytail: whole-file-in-envelope transfer; if this cap bites real
// projects, add chunked streaming (see docs/PROTOCOL.md Slice 6).
const MaxFileBytes = 24 << 20

// MaxMessageBytes is the WebSocket read limit applied on both ends of the
// control-plane connection. It bounds the largest legal envelope — a
// file.command/file.result carrying MaxFileBytes base64-encoded is ~33 MiB
// after encoding, so the limit is set above that.
const MaxMessageBytes = 48 << 20

// MaxEditorFileBytes caps the size of one file opened or saved through the
// Code tab's editor stream. It is far below MaxFileBytes because the editor
// autosaves the whole buffer on every debounce tick — a 24 MiB cap would put
// ~33 MiB base64 envelopes on the wire per keystroke burst. Files REST still
// carries up to MaxFileBytes; oversized files open there.
// ponytail: whole-buffer autosave; chunked/partial sync if 2 MiB bites.
const MaxEditorFileBytes = 2 << 20

// FileCommandPayload is the payload of a MessageFileCommand envelope.
//
// Root is the absolute directory every operation is contained in, resolved
// by the Control Server from the Project record — never taken from a client
// request. Path and To are always relative to Root; the runtime re-validates
// them and re-checks containment via os.Root regardless of what the Control
// Server already checked (docs/SECURITY.md, defense in depth).
type FileCommandPayload struct {
	// Action is one of: list, stat, read, write, create, mkdir, rename,
	// copy, move, delete, search, chmod, chown.
	Action string `json:"action"`
	Root   string `json:"root"`
	// Path is the target entry, relative to Root. "." refers to the root
	// itself (used by "list" for directory navigation).
	Path string `json:"path,omitempty"`
	// To is the destination for rename/copy/move, relative to the same Root.
	To string `json:"to,omitempty"`
	// Data carries the file bytes for a "write" action (base64-encoded on
	// the wire by encoding/json).
	Data []byte `json:"data,omitempty"`
	// Query is the name substring for a "search" action.
	Query string `json:"query,omitempty"`
	// DirsOnly restricts a "search" action's matches to directories.
	DirsOnly bool `json:"dirs_only,omitempty"`
	// Mode is a "chmod" action's new permission bits, as an octal string
	// (e.g. "755"). Only the low 9 bits (owner/group/other rwx) are
	// honored — setuid/setgid/sticky are intentionally not settable through
	// this path.
	Mode string `json:"mode,omitempty"`
	// Owner and Group are a "chown" action's new username/group name. Either
	// may be empty to leave that half unchanged (POSIX chown's -1 uid/gid
	// convention). Resolved to a uid/gid by the runtime, which is also where
	// the OS-level permission check happens — a non-root runtime can only
	// ever succeed at changing the group to one it belongs to, and cannot
	// change the owning user at all (ADR-032).
	Owner string `json:"owner,omitempty"`
	Group string `json:"group,omitempty"`
	// Recursive applies a "chmod" or "chown" action to every entry beneath
	// Path as well, when Path is a directory.
	Recursive bool `json:"recursive,omitempty"`
}

// FileInfo describes one filesystem entry.
type FileInfo struct {
	Name string `json:"name"`
	// Type is "file", "dir", "symlink", or "other".
	Type string `json:"type"`
	Size int64  `json:"size"`
	// Mode is the POSIX mode string, e.g. "-rw-r--r--".
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mtime"`
	// Owner/Group are the resolved account/group names for OwnerID/GroupID,
	// empty when the uid/gid has no matching /etc/passwd or /etc/group
	// entry (a deleted account, a container image with a bare numeric ID).
	Owner   string `json:"owner,omitempty"`
	OwnerID int    `json:"owner_id"`
	Group   string `json:"group,omitempty"`
	GroupID int    `json:"group_id"`
}

// FileResultPayload is the payload of a MessageFileResult envelope.
// Exactly one of Entries, Info, Data, or Matches is populated, matching the
// request's Action.
type FileResultPayload struct {
	Entries []FileInfo `json:"entries,omitempty"`
	Info    *FileInfo  `json:"info,omitempty"`
	Data    []byte     `json:"data,omitempty"`
	Matches []string   `json:"matches,omitempty"`
}

// GitCommandPayload is the payload of a MessageGitCommand envelope.
//
// Root is the Project's working directory, resolved by the Control Server
// from the Project record — never taken from a client request, same rule as
// FileCommandPayload.Root. Path is root-relative; the runtime scopes every
// read (status/diff) to it with a git pathspec rather than the whole
// repository, in case Root is a subdirectory of a larger git checkout.
type GitCommandPayload struct {
	// Action is one of: status, diff, stage, unstage, commit, log, pull,
	// push, discard, amend, branches, checkout, branch.
	Action string `json:"action"`
	Root   string `json:"root"`
	// Path scopes a "diff"/"stage"/"unstage"/"discard" request, relative to
	// Root. "" or "." means the whole Project root for "diff"; required
	// (non-empty) for "stage"/"unstage"/"discard". For "checkout"/"branch"
	// it is the branch name instead (a single git argv token).
	Path string `json:"path,omitempty"`
	// Staged selects `git diff --cached` (staged vs HEAD) instead of the
	// default working-tree-vs-index diff, for a "diff" request.
	Staged bool `json:"staged,omitempty"`
	// Message is the commit message for a "commit" request, passed to `git
	// commit -F -` over stdin rather than argv (docs/SECURITY.md — never
	// logged as a process argument).
	Message string `json:"message,omitempty"`
	// Limit is the commit count for a "log" request.
	Limit int `json:"limit,omitempty"`
}

// GitFileStatus describes one changed path from a "status" request. Index is
// the staged-change status code, Worktree is the unstaged one — the two
// halves of git's XY status pair (git-status(1) "Porcelain Format Version
// 2"): each is ".", "M", "A", "D", "R", "C", "U", or "?" (untracked).
// OrigPath is set only for a rename/copy, to the path before the change.
type GitFileStatus struct {
	Path     string `json:"path"`
	OrigPath string `json:"orig_path,omitempty"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
}

// GitBranchStatus reports the current branch's upstream tracking state, set
// only on a "status" request's result. Parsed from `git status
// --porcelain=v2 --branch`'s header lines rather than a separate `git
// rev-list --left-right --count` call.
type GitBranchStatus struct {
	// Name is the current branch, "" for a detached HEAD.
	Name string `json:"name"`
	// Upstream is e.g. "origin/main", "" if the branch has no tracking
	// branch (never pushed, or pushed without -u).
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	// HasRemote is true if `git remote` reports at least one remote — used
	// to tell "no upstream yet" (push offers to set one) apart from "no
	// remote at all" (pull/push should be disabled), which otherwise look
	// identical from Upstream alone.
	HasRemote bool `json:"has_remote"`
}

// GitBranchInfo is one local branch (or, display-only, one remote branch)
// from a "branches" request.
type GitBranchInfo struct {
	Name string `json:"name"`
	// Current is true when the branch is checked out ("*" in %(HEAD) from
	// git for-each-ref); always false for remote entries.
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
}

// GitResultPayload is the payload of a MessageGitResult envelope. Which
// fields are populated depends on the request's Action ("stage"/"unstage"
// populate none of them — success is the empty result).
// GitCommitInfo is one recent commit from a "log" request.
type GitCommitInfo struct {
	SHA      string    `json:"sha"`
	Subject  string    `json:"subject"`
	Author   string    `json:"author"`
	Datetime time.Time `json:"datetime"`
}

type GitResultPayload struct {
	Files   []GitFileStatus  `json:"files,omitempty"`
	Branch  *GitBranchStatus `json:"branch,omitempty"`
	Diff    string           `json:"diff,omitempty"`
	Commit  string           `json:"commit,omitempty"`
	Commits []GitCommitInfo  `json:"commits,omitempty"`
	// Branches is set by a "branches" request: local branches (current one
	// marked) followed by remote branches, display-only.
	Branches []GitBranchInfo `json:"branches,omitempty"`
	// PushedNewUpstream is true when a "push" request had no upstream
	// configured and set one (`push -u origin HEAD`) — lets the UI report
	// "published this branch" instead of a plain "pushed".
	PushedNewUpstream bool `json:"pushed_new_upstream,omitempty"`
	// Conflicts is set by a "pull" request that resulted in a merge
	// conflict. This is not a git.error: the pull ran, git left the
	// worktree in a conflicted-but-inspectable state (same as running `git
	// pull` by hand), and Files/Conflicts are populated from a follow-up
	// status read instead. A pull that fails for any other reason (no
	// remote, auth, network) is still a git.error.
	Conflicts []GitFileStatus `json:"conflicts,omitempty"`
}

// CodeCommandPayload is the payload of a MessageCodeCommand envelope.
//
// Root is the Project's working directory, resolved by the Control Server
// from the Project record — never taken from a client request, same rule as
// FileCommandPayload.Root. Path and Paths entries are always relative to
// Root; the runtime re-validates containment via os.Root regardless.
type CodeCommandPayload struct {
	// Action is one of: open, save, watch, unwatch.
	Action string `json:"action"`
	Root   string `json:"root"`
	// Path is the target file for an open/save action, relative to Root.
	Path string `json:"path,omitempty"`
	// Paths carries a "watch" action's full replacement set (the Control
	// Server always sends the per-root union of every subscriber's open
	// files) or an "unwatch" action's paths to remove, relative to Root.
	Paths []string `json:"paths,omitempty"`
	// Data carries the whole buffer for a "save" action (base64-encoded on
	// the wire by encoding/json).
	Data []byte `json:"data,omitempty"`
	// BaseMTime/BaseSize are a "save" action's client-known on-disk
	// fingerprint — the mtime/size the client last saw saved or received. A
	// mismatch means something edited the file outside the editor since; the
	// runtime still writes (last-write-wins) but returns the pre-write
	// content with Stale set so the editor can prompt.
	BaseMTime time.Time `json:"base_mtime,omitempty"`
	BaseSize  int64     `json:"base_size"`
}

// CodeResultPayload is the payload of a MessageCodeResult envelope, matching
// the request's Action: "open" populates Data/MTime/Size/Binary; "save"
// populates MTime/Size plus Stale and (on a stale save) the pre-write
// disk content in Data; "watch"/"unwatch" populate none.
type CodeResultPayload struct {
	Data   []byte    `json:"data,omitempty"`
	MTime  time.Time `json:"mtime"`
	Size   int64     `json:"size"`
	Binary bool      `json:"binary,omitempty"`
	Stale  bool      `json:"stale,omitempty"`
}

// CodeFileChangedPayload is the payload of a MessageCodeFileChanged push:
// one watched path whose stat no longer matches the edit manager's last
// known mtime/size, or that vanished from disk (Deleted).
type CodeFileChangedPayload struct {
	Root    string    `json:"root"`
	Path    string    `json:"path"`
	MTime   time.Time `json:"mtime,omitempty"`
	Size    int64     `json:"size,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
}

// sessionIDFrameLen is the fixed width of the session ID prefix on a binary
// terminal frame. Session IDs are Postgres uuid values rendered as their
// canonical 36-character string form, so the width is constant and no
// length-prefix or delimiter is needed.
const sessionIDFrameLen = 36

// EncodeFrame builds a binary-safe terminal frame: a fixed-width session ID
// prefix followed by raw payload bytes. Terminal input/output must never be
// JSON-escaped (see docs/PROTOCOL.md), so frames travel as WebSocket binary
// messages alongside the JSON envelope used for control messages.
func EncodeFrame(sessionID string, payload []byte) []byte {
	frame := make([]byte, sessionIDFrameLen+len(payload))
	copy(frame, sessionID)
	copy(frame[sessionIDFrameLen:], payload)
	return frame
}

// DBConnectionParams identifies and authenticates a database connection.
// Built server-side from a stored, decrypted database_connections row —
// never taken from a client request (docs/SECURITY.md: database credentials
// are sensitive, and must never be trusted from the browser).
type DBConnectionParams struct {
	// Engine is one of: postgresql, mysql, mssql, sqlite.
	Engine       string `json:"engine"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Database     string `json:"database,omitempty"`
	SSLMode      string `json:"ssl_mode,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
	SearchPath   string `json:"search_path,omitempty"`
	ExtraParams  string `json:"extra_params,omitempty"`
}

// DBCommandPayload is the payload of a MessageDBCommand envelope.
type DBCommandPayload struct {
	// Action is one of: test, invalidate, schemas, tables, columns,
	// indexes, constraints, triggers (Slice 7/8), rows, commit, query
	// (Slice 9), principals, grants, create_principal, drop_principal,
	// set_principal_password, grant_privilege, revoke_privilege (Slice 10).
	// "invalidate" is a fire-and-forget notice (sent via SendEnvelope, no
	// reply) telling the runtime to drop its cached connection pool for
	// ConnectionID — see ADR-021/022.
	Action string `json:"action"`
	// ConnectionID keys the runtime's connection pool cache — a stored
	// connection is opened at most once per runtime process and reused
	// across schema-browsing RPCs, not reopened per request.
	ConnectionID string             `json:"connection_id"`
	Connection   DBConnectionParams `json:"connection"`
	// Schema/Table scope a schemas/tables/columns/indexes/constraints/
	// triggers/rows/commit request, and also a grant_privilege/
	// revoke_privilege request's target table. Schema is "" for an engine
	// with no schema concept (SQLite) or a single implicit one
	// (MySQL/MariaDB).
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table,omitempty"`
	// Limit/Offset/Sort/SortDesc/Filter scope a "rows" request. Filter is a
	// raw SQL boolean expression, not a parameterized value — the same
	// deliberate arbitrary-SQL trust model as SQL (ADR-021).
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Sort     string `json:"sort,omitempty"`
	SortDesc bool   `json:"sort_desc,omitempty"`
	Filter   string `json:"filter,omitempty"`
	// Edits carries a "commit" request's staged inserts/updates/deletes.
	Edits []DBRowEdit `json:"edits,omitempty"`
	// SQL carries a "query" request's arbitrary SQL Editor text.
	SQL string `json:"sql,omitempty"`
	// Principal names the target user/login/role for a grants/
	// create_principal/drop_principal/set_principal_password/
	// grant_privilege/revoke_privilege request (Slice 10).
	Principal string `json:"principal,omitempty"`
	// Password carries a create_principal/set_principal_password request's
	// new plaintext password — never logged (docs/SECURITY.md).
	Password string `json:"password,omitempty"`
	// CanLogin/Superuser scope a create_principal request.
	CanLogin  bool `json:"can_login,omitempty"`
	Superuser bool `json:"superuser,omitempty"`
	// Privilege scopes a grant_privilege/revoke_privilege request (Schema/
	// Table above name the target).
	Privilege string `json:"privilege,omitempty"`
}

// DBRowEdit is one staged insert/update/delete for a "commit" request.
type DBRowEdit struct {
	// Op is "insert", "update", or "delete".
	Op string `json:"op"`
	// Original identifies the row being updated/deleted — the column
	// values as originally fetched by "rows", used to build a WHERE
	// clause. Unused for insert.
	Original map[string]any `json:"original,omitempty"`
	// Values are the new column values for insert/update. Unused for
	// delete.
	Values map[string]any `json:"values,omitempty"`
}

// DBTableInfo describes one table or view.
type DBTableInfo struct {
	Name string `json:"name"`
	// Kind is "table" or "view".
	Kind string `json:"kind"`
}

// DBColumnInfo describes one table/view column.
type DBColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
	Position int    `json:"position"`
}

// DBIndexInfo describes one table index.
type DBIndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
}

// DBConstraintInfo describes one table constraint.
type DBConstraintInfo struct {
	Name string `json:"name"`
	// Type is "primary_key", "foreign_key", "unique", or "check".
	Type    string   `json:"type"`
	Columns []string `json:"columns"`
	// References is "schema.table(column)" for a foreign_key constraint,
	// otherwise "".
	References string `json:"references,omitempty"`
}

// DBTriggerInfo describes one table trigger. Event/Timing are best-effort —
// not every engine exposes them as cleanly as a name.
type DBTriggerInfo struct {
	Name   string `json:"name"`
	Event  string `json:"event,omitempty"`
	Timing string `json:"timing,omitempty"`
}

// DBRowsResult is the result of a "rows" or "query" request. Exactly one of
// (Columns+Rows) or (IsExec+RowsAffected) is meaningful, matching whether
// the statement produced a result set.
type DBRowsResult struct {
	Columns []string `json:"columns,omitempty"`
	// Rows holds each row's values in Columns order, JSON-marshaled as-is
	// (numbers/strings/bools/null/RFC3339 timestamps; a []byte value that
	// wasn't valid UTF-8 text is base64-encoded by encoding/json).
	Rows [][]any `json:"rows,omitempty"`
	// HasMore is true when more rows exist beyond Limit.
	HasMore      bool  `json:"has_more,omitempty"`
	IsExec       bool  `json:"is_exec,omitempty"`
	RowsAffected int64 `json:"rows_affected,omitempty"`
}

// DBPrincipalInfo describes one database-level user, login, or role.
type DBPrincipalInfo struct {
	Name      string `json:"name"`
	CanLogin  bool   `json:"can_login"`
	Superuser bool   `json:"superuser"`
}

// DBGrantInfo describes one granted privilege. Raw is set instead of the
// structured fields for an engine that only exposes its own native grant
// statement text rather than one row per privilege (MySQL's SHOW GRANTS).
type DBGrantInfo struct {
	Privilege string `json:"privilege,omitempty"`
	Schema    string `json:"schema,omitempty"`
	Table     string `json:"table,omitempty"`
	Raw       string `json:"raw,omitempty"`
}

// DBResultPayload is the payload of a MessageDBResult envelope. Exactly one
// of the non-Ok fields is populated, matching the request's Action.
type DBResultPayload struct {
	Ok          bool               `json:"ok,omitempty"`
	Schemas     []string           `json:"schemas,omitempty"`
	Tables      []DBTableInfo      `json:"tables,omitempty"`
	Columns     []DBColumnInfo     `json:"columns,omitempty"`
	Indexes     []DBIndexInfo      `json:"indexes,omitempty"`
	Constraints []DBConstraintInfo `json:"constraints,omitempty"`
	Triggers    []DBTriggerInfo    `json:"triggers,omitempty"`
	Rows        *DBRowsResult      `json:"rows,omitempty"`
	Principals  []DBPrincipalInfo  `json:"principals,omitempty"`
	Grants      []DBGrantInfo      `json:"grants,omitempty"`
}

// DBErrorPayload is the payload of a MessageDBError envelope. Same shape as
// ServiceErrorPayload/FileErrorPayload, kept as its own type for symmetry
// with the other db.* messages.
type DBErrorPayload struct {
	Error string `json:"error"`
}

// BackupInfo describes one stored backup file on a runtime host (ADR-041).
type BackupInfo struct {
	// FileName is a path relative to the runtime's backups root
	// (~/.local/share/devstationone/backups/) — never absolute.
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"` // RFC3339
}

// BackupCommandPayload is the payload of a MessageBackupCommand envelope.
type BackupCommandPayload struct {
	// Action is one of:
	//   start_db      — dump a database connection (Params + BackupID)
	//   start_project — archive a project directory (SourceDir + BackupID)
	//   status        — one job's state (JobID)
	//   list          — list stored backup files (under KindDir)
	//   delete        — remove a stored backup file (FileName)
	//   read          — one download chunk (FileName + Offset + Length)
	//   restore_db    — restore a DB backup into TargetDatabase (FileName + Params + TargetDatabase)
	//   restore_project — extract a project backup (FileName + RestoreDir, overwrite mode)
	Action string `json:"action"`
	// KindDir scopes list/restore/delete to one subtree of the backups
	// root: "db" or "projects".
	KindDir string `json:"kind_dir,omitempty"`
	// Params carries the connection to dump (start_db) or the credentials
	// the dump/restore runs under (restore_db) — resolved server-side from
	// a stored, decrypted database_connections row, never from a client
	// request (same trust model as DBCommandPayload.Connection).
	Params *DBConnectionParams `json:"params,omitempty"`
	// BackupID is the caller-assigned backup file base name (a UUID) — the
	// runtime appends the engine-specific extension.
	BackupID string `json:"backup_id,omitempty"`
	// FileName identifies a stored backup (delete/read/restore), relative
	// to the backups root (KindDir/BackupID.<ext>).
	FileName string `json:"file_name,omitempty"`
	// SourceDir is the absolute working directory a start_project request
	// archives. Resolved server-side from the Project record.
	SourceDir string `json:"source_dir,omitempty"`
	// RestoreDir is the absolute directory a restore_project extracts into.
	RestoreDir string `json:"restore_dir,omitempty"`
	// TargetDatabase names the database a restore_db writes into.
	TargetDatabase string `json:"target_database,omitempty"`
	// JobID scopes a status request; assigned by the runtime for start_*.
	JobID  string `json:"job_id,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Length int    `json:"length,omitempty"`
}

// BackupResultPayload is the payload of a MessageBackupResult envelope.
// Exactly the fields the requested action produces are populated.
type BackupResultPayload struct {
	JobID string `json:"job_id,omitempty"`
	// State is a job's state for status: running, done, or failed.
	State string       `json:"state,omitempty"`
	Error string       `json:"error,omitempty"`
	Size  int64        `json:"size,omitempty"`
	Files []BackupInfo `json:"files,omitempty"`
	// Data is one read chunk's bytes (base64-encoded on the wire).
	Data []byte `json:"data,omitempty"`
}

// DecodeFrame splits a binary terminal frame produced by EncodeFrame back
// into its session ID and payload. ok is false if frame is shorter than the
// session ID prefix.
func DecodeFrame(frame []byte) (sessionID string, payload []byte, ok bool) {
	if len(frame) < sessionIDFrameLen {
		return "", nil, false
	}
	return string(frame[:sessionIDFrameLen]), frame[sessionIDFrameLen:], true
}
