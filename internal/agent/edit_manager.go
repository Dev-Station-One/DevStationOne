package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/filesystem"
	"devstationone/internal/protocol"
)

// pollInterval is how often the edit manager re-stats the watched file set.
// A package var so tests can shorten it.
var pollInterval = time.Second

// editEntry is the edit manager's last known stat of one watched file.
type editEntry struct {
	mtime  time.Time
	size   int64
	exists bool
}

// editManager implements the code.command half of connection.Manager: it
// opens and saves whole files inside a Project root (internal/filesystem
// containment), and pushes code.file.changed envelopes when a watched file
// changes on disk — observed by polling mtime+size, not by inotify
// (docs/CODE_TAB_REQUIREMENTS.md: the open set is tiny, so a 1s stat poll
// beats watch-management complexity).
type editManager struct {
	logger *slog.Logger

	mu      sync.Mutex
	sender  connection.Sender
	watched map[string]map[string]editEntry // root -> path -> last known stat
	polling bool
}

func newEditManager(logger *slog.Logger) *editManager {
	return &editManager{logger: logger, watched: make(map[string]map[string]editEntry)}
}

func (m *editManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *editManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one code.command message. Like fileManager, it
// runs each command in its own goroutine so a large transfer never blocks
// the connection's read loop.
func (m *editManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageCodeCommand {
		return
	}
	var cmd protocol.CodeCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	go m.run(env.ID, cmd)
}

func (m *editManager) run(id string, cmd protocol.CodeCommandPayload) {
	var result protocol.CodeResultPayload
	var err error
	switch cmd.Action {
	case "open":
		result, err = m.open(cmd)
	case "save":
		result, err = m.save(cmd)
	case "watch":
		result, err = m.replaceWatch(cmd.Root, cmd.Paths)
	case "unwatch":
		result, err = m.removeWatch(cmd.Root, cmd.Paths)
	default:
		err = fmt.Errorf("unknown code action %q", cmd.Action)
	}
	m.reply(id, cmd, result, err)
}

func (m *editManager) reply(id string, cmd protocol.CodeCommandPayload, result protocol.CodeResultPayload, err error) {
	sender := m.currentSender()
	if sender == nil {
		return
	}
	if err != nil {
		if replyErr := sender.SendReply(id, protocol.MessageCodeError, protocol.ServiceErrorPayload{Error: err.Error()}); replyErr != nil {
			m.logger.Warn("send code error reply failed", "action", cmd.Action, "error", replyErr)
		}
		return
	}
	if replyErr := sender.SendReply(id, protocol.MessageCodeResult, result); replyErr != nil {
		m.logger.Warn("send code result reply failed", "action", cmd.Action, "error", replyErr)
	}
}

// open stats and reads one file, refusing directories, oversized files, and
// binary content (NUL byte in the first 8 KiB — the same sniff the Files-tab
// modal editor uses).
func (m *editManager) open(cmd protocol.CodeCommandPayload) (protocol.CodeResultPayload, error) {
	if cmd.Root == "" || cmd.Path == "" {
		return protocol.CodeResultPayload{}, fmt.Errorf("open requires root and path")
	}
	f, err := filesystem.Open(cmd.Root)
	if err != nil {
		return protocol.CodeResultPayload{}, err
	}
	defer f.Close()

	info, err := f.Stat(cmd.Path)
	if err != nil {
		return protocol.CodeResultPayload{}, err
	}
	if info.Type == "dir" {
		return protocol.CodeResultPayload{}, fmt.Errorf("%q is a directory", cmd.Path)
	}
	if info.Size > protocol.MaxEditorFileBytes {
		return protocol.CodeResultPayload{}, fmt.Errorf("%q is %d bytes; the editor handles files up to %d bytes", cmd.Path, info.Size, protocol.MaxEditorFileBytes)
	}
	data, err := f.Read(cmd.Path)
	if err != nil {
		return protocol.CodeResultPayload{}, err
	}
	if containsNUL(data) {
		return protocol.CodeResultPayload{MTime: info.ModTime, Size: info.Size, Binary: true}, nil
	}
	return protocol.CodeResultPayload{Data: data, MTime: info.ModTime, Size: info.Size}, nil
}

// save writes the client's whole buffer, comparing the file's current
// mtime/size against the client's base fingerprint first. On a mismatch it
// reads the pre-write disk content and returns it with Stale set (the client
// buffer is written anyway — last-write-wins) so the editor can offer the
// overwritten version in its conflict prompt.
func (m *editManager) save(cmd protocol.CodeCommandPayload) (protocol.CodeResultPayload, error) {
	if cmd.Root == "" || cmd.Path == "" {
		return protocol.CodeResultPayload{}, fmt.Errorf("save requires root and path")
	}
	f, err := filesystem.Open(cmd.Root)
	if err != nil {
		return protocol.CodeResultPayload{}, err
	}
	defer f.Close()

	var staleData []byte
	stale := false
	info, statErr := f.Stat(cmd.Path)
	switch {
	case statErr == nil && (!info.ModTime.Equal(cmd.BaseMTime) || info.Size != cmd.BaseSize):
		stale = true
		// Capture the external version before it is overwritten.
		if pre, readErr := f.Read(cmd.Path); readErr == nil && len(pre) <= protocol.MaxEditorFileBytes {
			staleData = pre
		}
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return protocol.CodeResultPayload{}, statErr
	}

	if err := f.Write(cmd.Path, cmd.Data); err != nil {
		return protocol.CodeResultPayload{}, err
	}

	// Record the post-save stat so the poll loop never re-pushes our own
	// write back out.
	saved, err := f.Stat(cmd.Path)
	if err != nil {
		return protocol.CodeResultPayload{Stale: stale, Data: staleData}, nil
	}
	m.mu.Lock()
	if root := m.watched[cmd.Root]; root != nil {
		if _, isWatched := root[cmd.Path]; isWatched {
			root[cmd.Path] = editEntry{mtime: saved.ModTime, size: saved.Size, exists: true}
		}
	}
	m.mu.Unlock()

	return protocol.CodeResultPayload{MTime: saved.ModTime, Size: saved.Size, Stale: stale, Data: staleData}, nil
}

// replaceWatch swaps a root's watched set for paths, preserving the last
// known stat of retained paths so a retained file is not re-pushed. Newly
// watched paths start with no known stat; the poll loop seeds them on its
// first tick without pushing.
func (m *editManager) replaceWatch(root string, paths []string) (protocol.CodeResultPayload, error) {
	if root == "" {
		return protocol.CodeResultPayload{}, fmt.Errorf("watch requires root")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	set := make(map[string]editEntry, len(paths))
	for _, p := range paths {
		// Previously known paths keep their stat (no spurious re-push);
		// new paths get the zero entry and are seeded silently by the
		// first poll tick.
		set[p] = m.watched[root][p]
	}
	m.watched[root] = set
	m.ensurePollingLocked()
	return protocol.CodeResultPayload{}, nil
}

// removeWatch drops paths from a root's watched set, dropping the root when
// nothing is left.
func (m *editManager) removeWatch(root string, paths []string) (protocol.CodeResultPayload, error) {
	if root == "" {
		return protocol.CodeResultPayload{}, fmt.Errorf("unwatch requires root")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range paths {
		delete(m.watched[root], p)
	}
	if len(m.watched[root]) == 0 {
		delete(m.watched, root)
	}
	return protocol.CodeResultPayload{}, nil
}

// ensurePollingLocked starts the poll goroutine on first use; it lives for
// the runtime process once started and exits only when nothing is watched.
func (m *editManager) ensurePollingLocked() {
	if m.polling {
		return
	}
	m.polling = true
	go m.pollLoop()
}

func (m *editManager) pollLoop() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		if len(m.watched) == 0 {
			m.polling = false
			m.mu.Unlock()
			return
		}
		roots := make([]string, 0, len(m.watched))
		for root := range m.watched {
			roots = append(roots, root)
		}
		m.mu.Unlock()

		for _, root := range roots {
			m.pollRoot(root)
		}
	}
}

// pollRoot stats every watched path under root through the same os.Root
// containment the file manager uses, and pushes code.file.changed for any
// path whose stat drifted from the last known mtime/size.
func (m *editManager) pollRoot(root string) {
	f, err := filesystem.Open(root)
	if err != nil {
		m.logger.Warn("edit poll: open root failed", "root", root, "error", err)
		return
	}
	defer f.Close()

	m.mu.Lock()
	paths := make([]string, 0, len(m.watched[root]))
	for p := range m.watched[root] {
		paths = append(paths, p)
	}
	m.mu.Unlock()

	for _, p := range paths {
		info, err := f.Stat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				m.recordAndPush(root, p, editEntry{}, true)
			}
			continue
		}
		if info.Type == "dir" {
			continue
		}
		m.recordAndPush(root, p, editEntry{mtime: info.ModTime, size: info.Size, exists: true}, false)
	}
}

// recordAndPush updates one path's recorded stat, pushing code.file.changed
// when the new stat differs from a previously observed one. A path whose
// only recorded state is the zero entry (seeded but never stat'ed) is
// recorded silently — its first observation is not a change.
func (m *editManager) recordAndPush(root, path string, entry editEntry, deleted bool) {
	m.mu.Lock()
	prev, ok := m.watched[root][path]
	if ok && sameStat(entry, prev) {
		m.mu.Unlock()
		return
	}
	m.watched[root][path] = entry
	sender := m.sender
	// Only paths we have actually observed a stat for (or observed deleted)
	// can produce a change push.
	observed := ok && (prev.exists || !prev.mtime.IsZero())
	m.mu.Unlock()

	if !observed || sender == nil {
		return
	}
	push := protocol.CodeFileChangedPayload{Root: root, Path: path}
	if deleted {
		push.Deleted = true
	} else {
		push.MTime = entry.mtime
		push.Size = entry.size
	}
	if err := sender.SendEnvelope(protocol.MessageCodeFileChanged, push); err != nil {
		m.logger.Warn("send file changed push failed", "path", path, "error", err)
	}
}

func sameStat(a, b editEntry) bool {
	return a.exists == b.exists && a.size == b.size && a.mtime.Equal(b.mtime)
}

// containsNUL reports whether data contains a NUL byte within its first
// 8 KiB — the editor's binary sniff (Files-tab modal editor convention).
func containsNUL(data []byte) bool {
	limit := len(data)
	if limit > 8<<10 {
		limit = 8 << 10
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}