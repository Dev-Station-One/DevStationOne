package agent

import (
	"archive/tar"
	"compress/gzip"

	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"devstationone/internal/connection"
	"devstationone/internal/protocol"
)

// backupChunkSize is how many bytes one "read" action returns. Small enough
// that the JSON envelope (base64 inflates by ~4/3) stays far under
// protocol.MaxMessageBytes.
const backupChunkSize = 4 << 20 // 4 MiB

// BackupsRoot returns the fixed backup storage root on this runtime host
// (ADR-041): ~/.local/share/devstationone/backups, with db/ and projects/
// subtrees. Exported as a var (not a plain func) so integration tests — in
// this package and in internal/api, which runs a real dispatcher against a
// real fake runtime connection — can point it at a temp directory instead
// of the real home directory.
var BackupsRoot = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "devstationone", "backups"), nil
}

// backupJob tracks one in-flight or finished start_* operation.
type backupJob struct {
	state string // running | done | failed
	err   string
	size  int64
}

// backupManager implements the backup.command half of connection.Manager
// (ADR-042): it creates database dumps and project archives on the runtime
// host, restores them, and serves chunked reads for download. start_*
// actions reply immediately with a JobID and run the job in a goroutine —
// the Control Server polls "status" for completion.
type backupManager struct {
	logger *slog.Logger

	mu     sync.Mutex
	sender connection.Sender
	jobs   map[string]*backupJob
}

func newBackupManager(logger *slog.Logger) *backupManager {
	return &backupManager{logger: logger, jobs: make(map[string]*backupJob)}
}

func (m *backupManager) SetSender(s connection.Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

func (m *backupManager) currentSender() connection.Sender {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// HandleEnvelope handles one backup.command message. Synchronous actions
// (status/list/delete/read) run inline; start_* run in a goroutine and reply
// first; restore replies only on completion — the Control Server's restore
// orchestrator waits on the reply, since the next step depends on the
// outcome.
func (m *backupManager) HandleEnvelope(env protocol.Envelope) {
	if env.Type != protocol.MessageBackupCommand {
		return
	}
	var cmd protocol.BackupCommandPayload
	if json.Unmarshal(env.Payload, &cmd) != nil {
		return
	}
	switch cmd.Action {
	case "start_db", "start_project":
		jobID := newJobID()
		m.mu.Lock()
		m.jobs[jobID] = &backupJob{state: "running"}
		m.mu.Unlock()
		m.reply(env.ID, protocol.BackupResultPayload{JobID: jobID})
		go m.runJob(env.ID, jobID, cmd)
	case "restore_db", "restore_project":
		go m.runRestore(env.ID, cmd)
	case "status":
		m.mu.Lock()
		j := m.jobs[cmd.JobID]
		m.mu.Unlock()
		if j == nil {
			m.replyErr(env.ID, fmt.Errorf("unknown job %q", cmd.JobID))
			return
		}
		m.reply(env.ID, protocol.BackupResultPayload{JobID: cmd.JobID, State: j.state, Error: j.err, Size: j.size})
	case "list":
		if err := validKindDir(cmd.KindDir); err != nil {
			m.replyErr(env.ID, err)
			return
		}
		files, err := m.list(cmd.KindDir)
		if err != nil {
			m.replyErr(env.ID, err)
			return
		}
		m.reply(env.ID, protocol.BackupResultPayload{Files: files})
	case "delete":
		if err := m.delete(cmd.FileName); err != nil {
			m.replyErr(env.ID, err)
			return
		}
		m.reply(env.ID, protocol.BackupResultPayload{})
	case "read":
		data, err := m.read(cmd)
		if err != nil {
			m.replyErr(env.ID, err)
			return
		}
		m.reply(env.ID, protocol.BackupResultPayload{Data: data})
	default:
		m.replyErr(env.ID, fmt.Errorf("unknown backup action %q", cmd.Action))
	}
}

func newJobID() string {
	return fmt.Sprintf("bk-%d", time.Now().UnixNano())
}

func (m *backupManager) setJob(id, state, errMsg string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// ponytail: jobs map grows for the process lifetime; entries are ~100
	// bytes and jobs are rare — sweep if this ever matters.
	m.jobs[id] = &backupJob{state: state, err: errMsg, size: size}
}

func (m *backupManager) reply(envID string, payload protocol.BackupResultPayload) {
	if sender := m.currentSender(); sender != nil {
		_ = sender.SendReply(envID, protocol.MessageBackupResult, payload)
	}
}

func (m *backupManager) replyErr(envID string, err error) {
	if sender := m.currentSender(); sender != nil {
		_ = sender.SendReply(envID, protocol.MessageBackupError, protocol.DBErrorPayload{Error: err.Error()})
	}
}

func (m *backupManager) runJob(envID, jobID string, cmd protocol.BackupCommandPayload) {
	size, err := m.execute(cmd)
	if err != nil {
		m.setJob(jobID, "failed", err.Error(), 0)
		m.logger.Warn("backup job failed", "action", cmd.Action, "error", err)
		return
	}
	m.setJob(jobID, "done", "", size)
}

// runRestore replies only when the restore finished (success or failure) —
// the Control Server's restore orchestrator waits on this reply, since the
// next step depends on the outcome.
func (m *backupManager) runRestore(envID string, cmd protocol.BackupCommandPayload) {
	var err error
	switch cmd.Action {
	case "restore_db":
		err = restoreDB(cmd)
	case "restore_project":
		err = restoreProject(cmd)
	}
	if err != nil {
		m.replyErr(envID, err)
		return
	}
	m.reply(envID, protocol.BackupResultPayload{})
}

func (m *backupManager) list(kindDir string) ([]protocol.BackupInfo, error) {
	root, err := BackupsRoot()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(root, kindDir))
	if os.IsNotExist(err) {
		return []protocol.BackupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]protocol.BackupInfo, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		inf, err := e.Info()
		if err != nil {
			continue // raced away; skip rather than fail the listing
		}
		files = append(files, protocol.BackupInfo{
			FileName: kindDir + "/" + e.Name(),
			Size:     inf.Size(),
			ModTime:  inf.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return files, nil
}

func (m *backupManager) delete(fileName string) error {
	name, err := cleanBackupFileName(fileName)
	if err != nil {
		return err
	}
	root, err := BackupsRoot()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Drop the project manifest sidecar too, if one exists.
	if strings.HasSuffix(name, ".tar.gz") {
		_ = os.Remove(filepath.Join(root, strings.TrimSuffix(name, ".tar.gz")+".json"))
	}
	return nil
}

func (m *backupManager) read(cmd protocol.BackupCommandPayload) ([]byte, error) {
	name, err := cleanBackupFileName(cmd.FileName)
	if err != nil {
		return nil, err
	}
	if cmd.Length <= 0 || cmd.Length > backupChunkSize {
		return nil, fmt.Errorf("length must be between 1 and %d", backupChunkSize)
	}
	root, err := BackupsRoot()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, cmd.Length)
	n, err := f.ReadAt(buf, cmd.Offset)
	if n == 0 && err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// cleanBackupFileName validates a stored-backup path: it must name a file
// inside the backups root (db/ or projects/ subtree), never escape it.
func cleanBackupFileName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("file_name is required")
	}
	c := filepath.Clean(name)
	if filepath.IsAbs(c) || strings.HasPrefix(c, "..") {
		return "", fmt.Errorf("file_name %q escapes the backups root", name)
	}
	dir, base := filepath.Split(c)
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	if (dir != "db" && dir != "projects") || base == "" || base == "." {
		return "", fmt.Errorf("file_name %q must be db/<id>.<ext> or projects/<id>.<ext>", name)
	}
	return c, nil
}

func validKindDir(kind string) error {
	if kind != "db" && kind != "projects" {
		return fmt.Errorf("kind_dir must be db or projects")
	}
	return nil
}

// execute runs a start_* job to completion and returns the backup's size.
func (m *backupManager) execute(cmd protocol.BackupCommandPayload) (int64, error) {
	if cmd.BackupID == "" || strings.ContainsAny(cmd.BackupID, "/\\") || strings.Contains(cmd.BackupID, "..") {
		return 0, fmt.Errorf("backup_id is required and must be a plain identifier")
	}
	root, err := BackupsRoot()
	if err != nil {
		return 0, err
	}
	switch cmd.Action {
	case "start_db":
		if cmd.Params == nil {
			return 0, fmt.Errorf("params is required")
		}
		out := filepath.Join(root, "db", cmd.BackupID+".dump")
		if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
			return 0, err
		}
		if err := dumpDB(cmd.Params, out); err != nil {
			_ = os.Remove(out)
			return 0, err
		}
		return fileSize(out)

	case "start_project":
		if cmd.SourceDir == "" {
			return 0, fmt.Errorf("source_dir is required")
		}
		out := filepath.Join(root, "projects", cmd.BackupID+".tar.gz")
		if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
			return 0, err
		}
		if err := archiveDir(cmd.SourceDir, out, cmd.BackupID); err != nil {
			_ = os.Remove(out)
			return 0, err
		}
		return fileSize(out)
	}
	return 0, fmt.Errorf("unknown backup action %q", cmd.Action)
}

func fileSize(p string) (int64, error) {
	inf, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return inf.Size(), nil
}

// --- database dump/restore ---

// dumpDB writes engine p's database to out using that engine's native dump
// tooling, executed on this runtime host. Tool stderr is returned in the
// error only on failure — dump data itself is never logged.
func dumpDB(p *protocol.DBConnectionParams, out string) error {
	switch p.Engine {
	case "postgresql":
		args := []string{"--dbname=" + pgConnInfo(p), "--format=custom", "--file=" + out}
		return runTool("pg_dump", args, pgEnv(p))
	case "mysql":
		outSQL := strings.TrimSuffix(out, ".dump") + ".sql"
		args := []string{"--single-transaction", "--host=" + p.Host, "--port=" + itoa(p.Port),
			"--user=" + p.Username, "--result-file=" + outSQL, p.Database}
		if err := runTool("mysqldump", args, mysqlEnv(p)); err != nil {
			if err2 := runTool("mariadb-dump", args, mysqlEnv(p)); err2 != nil {
				return err // report the mysqldump failure
			}
		}
		return os.Rename(outSQL, out)
	case "mssql":
		outBak := strings.TrimSuffix(out, ".dump") + ".bak"
		sql := fmt.Sprintf("BACKUP DATABASE [%s] TO DISK='%s' WITH INIT", p.Database, outBak)
		if err := runTool("sqlcmd", mssqlArgs(p, sql), nil); err != nil {
			return err
		}
		return os.Rename(outBak, out)
	case "sqlite":
		// SQLite's .backup command produces a consistent copy even of a
		// live database — never a plain cp.
		return runTool("sqlite3", []string{p.Database, ".backup '" + out + "'"}, nil)
	}
	return fmt.Errorf("unsupported engine %q", p.Engine)
}

func restoreDB(cmd protocol.BackupCommandPayload) error {
	p := cmd.Params
	if p == nil || cmd.FileName == "" || cmd.TargetDatabase == "" {
		return fmt.Errorf("params, file_name and target_database are required")
	}
	name, err := cleanBackupFileName(cmd.FileName)
	if err != nil {
		return err
	}
	root, err := BackupsRoot()
	if err != nil {
		return err
	}
	in := filepath.Join(root, name)

	switch p.Engine {
	case "postgresql":
		// Create the target if it doesn't exist yet; "already exists" is
		// fine (restore into an existing DB is allowed).
		_ = runTool("createdb", append(pgConnArgs(p), cmd.TargetDatabase), pgEnv(p))
		args := []string{"--dbname=" + pgConnInfo(p, cmd.TargetDatabase), "--clean", "--if-exists", in}
		return runTool("pg_restore", args, pgEnv(p))
	case "mysql":
		_ = runTool("mysqladmin", append(mysqlConnArgs(p), "create", cmd.TargetDatabase), mysqlEnv(p))
		f, err := os.Open(in)
		if err != nil {
			return err
		}
		defer f.Close()
		args := append(mysqlConnArgs(p), cmd.TargetDatabase)
		return runToolStdin("mysql", args, mysqlEnv(p), f)
	case "mssql":
		sql := fmt.Sprintf("RESTORE DATABASE [%s] FROM DISK='%s' WITH REPLACE", cmd.TargetDatabase, in)
		return runTool("sqlcmd", mssqlArgs(p, sql), nil)
	case "sqlite":
		return runTool("sqlite3", []string{cmd.TargetDatabase, fmt.Sprintf(".restore '%s'", in)}, nil)
	}
	return fmt.Errorf("unsupported engine %q", p.Engine)
}

// pgConnInfo builds a libpq keyword/value conninfo string. dbname defaults
// to the connection's own database when target is "".
func pgConnInfo(p *protocol.DBConnectionParams, target ...string) string {
	db := p.Database
	if len(target) > 0 {
		db = target[0]
	}
	parts := []string{"host=" + p.Host, "port=" + itoa(p.Port), "user=" + p.Username, "dbname=" + db}
	if p.SSLMode != "" {
		parts = append(parts, "sslmode="+p.SSLMode)
	}
	return strings.Join(parts, " ")
}

func pgConnArgs(p *protocol.DBConnectionParams) []string {
	args := []string{"--host=" + p.Host, "--port=" + itoa(p.Port), "--username=" + p.Username}
	return args
}

func pgEnv(p *protocol.DBConnectionParams) []string { return []string{"PGPASSWORD=" + p.Password} }

func mysqlConnArgs(p *protocol.DBConnectionParams) []string {
	return []string{"--host=" + p.Host, "--port=" + itoa(p.Port), "--user=" + p.Username}
}

func mysqlEnv(p *protocol.DBConnectionParams) []string { return []string{"MYSQL_PWD=" + p.Password} }

func mssqlArgs(p *protocol.DBConnectionParams, sql string) []string {
	return []string{"-S", p.Host + "," + itoa(p.Port), "-U", p.Username, "-P", p.Password, "-Q", sql}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// runTool executes name with args (env appended to os.Environ) and returns
// stderr in the error on failure.
func runTool(name string, args []string, env []string) error {
	return runToolStdin(name, args, env, nil)
}

func runToolStdin(name string, args []string, env []string, stdin io.Reader) error {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = stdin
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return fmt.Errorf("%s failed: %s", name, msg)
		}
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

// --- project archives ---

// backupManifest is the sidecar JSON written next to every project archive
// (design: tar.gz + sidecar manifest).
type backupManifest struct {
	Format     int    `json:"format"`
	BackupID   string `json:"backup_id"`
	SourceDir  string `json:"source_dir"`
	CreatedAt  string `json:"created_at"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

// archiveDir tars and gzips every entry under dir (nothing excluded —
// .gitignore is not honored; it's a backup, not a git snapshot) into out,
// writing the sidecar manifest next to it.
func archiveDir(sourceDir, out, backupID string) error {
	inf, err := os.Stat(sourceDir)
	if err != nil {
		return fmt.Errorf("source directory: %w", err)
	}
	if !inf.IsDir() {
		return fmt.Errorf("%q is not a directory", sourceDir)
	}

	manifest := backupManifest{
		Format:    1,
		BackupID:  backupID,
		SourceDir: sourceDir,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	outF, err := os.Create(out)
	if err != nil {
		return err
	}
	defer outF.Close()
	gz := gzip.NewWriter(outF)
	tw := tar.NewWriter(gz)

	count, bytes := 0, int64(0)
	err = filepath.WalkDir(sourceDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		// Store forward-slash relative names only; never absolute.
		hdr.Name = filepath.ToSlash(rel)
		hdr.Uname, hdr.Gname = "", "" // names are host-specific; uid/gid kept
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			n, err := io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
			count++
			bytes += n
		} else {
			count++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := outF.Sync(); err != nil {
		return err
	}

	manifest.FileCount = count
	manifest.TotalBytes = bytes
	mb, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(out, ".tar.gz")+".json", mb, 0o644)
}

// Restore ceilings against decompression bombs.
// ponytail: fixed caps, no config knob unless a real project needs more.
const (
	maxRestoreEntries       = 5_000_000
	maxRestoreBytes   int64 = 64 << 30
)

// restoreProject extracts the archive at cmd.FileName into cmd.RestoreDir,
// after validating its manifest. Every entry is containment-checked: names
// must stay inside the target, symlink targets must stay inside it too.
// The caller (Control Server) is responsible for having closed the project's
// sessions and captured a pre-restore snapshot before this runs.
func restoreProject(cmd protocol.BackupCommandPayload) error {
	if cmd.RestoreDir == "" {
		return fmt.Errorf("restore_dir is required")
	}
	name, err := cleanBackupFileName(cmd.FileName)
	if err != nil {
		return err
	}
	root, err := BackupsRoot()
	if err != nil {
		return err
	}
	archive := filepath.Join(root, name)

	manifestPath := strings.TrimSuffix(archive, ".tar.gz") + ".json"
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	if manifest.Format != 1 {
		return fmt.Errorf("unsupported backup format %d", manifest.Format)
	}

	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	absTarget, err := filepath.Abs(cmd.RestoreDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absTarget, 0o755); err != nil {
		return err
	}
	// Every write goes through an os.Root so that symlinks already present
	// in the restore directory (not just ones in the archive) cannot redirect
	// a write outside it — same containment as internal/filesystem.
	dir, err := os.OpenRoot(absTarget)
	if err != nil {
		return err
	}
	defer dir.Close()

	var entries int
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if entries++; entries > maxRestoreEntries {
			return fmt.Errorf("archive has more than %d entries", maxRestoreEntries)
		}
		// Containment check: reject absolute names, anything escaping the
		// target (rejected outright, never silently clamped to the root —
		// a malformed/hostile archive should fail loudly), and symlink
		// entries pointing outside it.
		name := hdr.Name
		if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
			return fmt.Errorf("archive entry %q is absolute", name)
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q escapes the restore directory", name)
		}
		if clean == "." {
			continue
		}
		if hdr.Typeflag == tar.TypeSymlink {
			linkDest := filepath.FromSlash(hdr.Linkname)
			if filepath.IsAbs(linkDest) {
				return fmt.Errorf("archive symlink %q is absolute", name)
			}
			resolved := filepath.Join(absTarget, filepath.Dir(clean), linkDest)
			if !strings.HasPrefix(resolved, absTarget+string(filepath.Separator)) {
				return fmt.Errorf("archive symlink %q escapes the restore directory", name)
			}
			if err := dir.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return err
			}
			_ = dir.Remove(clean)
			if err := dir.Symlink(linkDest, clean); err != nil {
				return err
			}
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := dir.MkdirAll(clean, os.FileMode(hdr.Mode)|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := dir.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return err
			}
			_ = dir.Remove(clean)
			outF, err := dir.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			n, err := io.Copy(outF, io.LimitReader(tr, maxRestoreBytes-total+1))
			outF.Close()
			if err != nil {
				return err
			}
			if total += n; total > maxRestoreBytes {
				return fmt.Errorf("archive expands beyond %d bytes", int64(maxRestoreBytes))
			}
		default:
			// Hard links, devices, fifos: skip rather than fail the whole
			// restore on entries a dev project archive realistically never
			// contains (devstationone archives only ever hold dir/symlink/
			// reg entries).
		}
	}
	return nil
}
