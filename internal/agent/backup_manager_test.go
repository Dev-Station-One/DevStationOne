package agent

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"devstationone/internal/protocol"
)

// withBackupsRoot points BackupsRoot at a temp directory for the duration
// of the test, restoring the real implementation after.
func withBackupsRoot(t *testing.T, root string) {
	t.Helper()
	orig := BackupsRoot
	BackupsRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { BackupsRoot = orig })
}

func TestCleanBackupFileName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid db", "db/abc123.dump", false},
		{"valid project", "projects/abc123.tar.gz", false},
		{"empty", "", true},
		{"absolute", "/etc/passwd", true},
		{"traversal", "db/../../../etc/passwd", true},
		{"wrong subtree", "other/abc123.dump", true},
		{"no file", "db/", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := cleanBackupFileName(c.in)
			if (err != nil) != c.wantErr {
				t.Errorf("cleanBackupFileName(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestArchiveDirRoundTrip(t *testing.T) {
	src := t.TempDir()
	// Every file is included — .gitignore is not honored (requirements).
	if err := os.WriteFile(filepath.Join(src, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "node_modules", "pkg", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	withBackupsRoot(t, root)
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "projects", "test1.tar.gz")
	if err := archiveDir(src, out, "test1"); err != nil {
		t.Fatalf("archiveDir: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	manifestPath := filepath.Join(root, "projects", "test1.json")
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		t.Fatalf("invalid manifest: %v", err)
	}
	if manifest.Format != 1 || manifest.BackupID != "test1" {
		t.Fatalf("manifest = %+v", manifest)
	}

	dest := t.TempDir()
	// restoreProject extracts under a fresh directory (the "new project"
	// path — never over an existing populated dir in this test).
	restoreDest := filepath.Join(dest, "restored")
	err = restoreProject(protocol.BackupCommandPayload{
		FileName: "projects/test1.tar.gz", RestoreDir: restoreDest,
	})
	if err != nil {
		t.Fatalf("restoreProject: %v", err)
	}

	for _, rel := range []string{".gitignore", "main.go", "node_modules/pkg/index.js"} {
		if _, err := os.Stat(filepath.Join(restoreDest, rel)); err != nil {
			t.Errorf("restored file %q missing: %v", rel, err)
		}
	}
}

// writeMaliciousArchive builds a tar.gz with the given entries directly
// (bypassing archiveDir) so a containment-violating entry can be crafted.
func writeMaliciousArchive(t *testing.T, root, backupID string, entries []tar.Header) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "projects", backupID+".tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := range entries {
		if err := tw.WriteHeader(&entries[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := backupManifest{Format: 1, BackupID: backupID}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "projects", backupID+".json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRestoreProjectRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	writeMaliciousArchive(t, root, "evil1", []tar.Header{
		{Name: "../../../etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	})

	dest := filepath.Join(t.TempDir(), "restored")
	err := restoreProject(protocol.BackupCommandPayload{FileName: "projects/evil1.tar.gz", RestoreDir: dest})
	if err == nil {
		t.Fatal("restoreProject accepted a path-traversal entry")
	}
}

func TestRestoreProjectRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	writeMaliciousArchive(t, root, "evil2", []tar.Header{
		{Name: "escape-link", Typeflag: tar.TypeSymlink, Linkname: "../../../etc", Mode: 0o777},
	})

	dest := filepath.Join(t.TempDir(), "restored")
	err := restoreProject(protocol.BackupCommandPayload{FileName: "projects/evil2.tar.gz", RestoreDir: dest})
	if err == nil {
		t.Fatal("restoreProject accepted a symlink escaping the restore directory")
	}
}

func TestRestoreProjectRejectsAbsoluteEntry(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	writeMaliciousArchive(t, root, "evil3", []tar.Header{
		{Name: "/etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	})

	dest := filepath.Join(t.TempDir(), "restored")
	err := restoreProject(protocol.BackupCommandPayload{FileName: "projects/evil3.tar.gz", RestoreDir: dest})
	if err == nil {
		t.Fatal("restoreProject accepted an absolute entry")
	}
}

func TestRestoreProjectRejectsUnsupportedManifestFormat(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "projects", "evil4.tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.Close()
	gz.Close()
	f.Close()
	mb, _ := json.Marshal(backupManifest{Format: 99, BackupID: "evil4"})
	if err := os.WriteFile(filepath.Join(root, "projects", "evil4.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	err = restoreProject(protocol.BackupCommandPayload{FileName: "projects/evil4.tar.gz", RestoreDir: dest})
	if err == nil {
		t.Fatal("restoreProject accepted an unsupported manifest format")
	}
}

func TestBackupManagerListDeleteRead(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", "b1.dump"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newBackupManager(testLogger(t))
	files, err := m.list("db")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 || files[0].FileName != "db/b1.dump" || files[0].Size != 11 {
		t.Fatalf("list = %+v", files)
	}

	data, err := m.read(protocol.BackupCommandPayload{FileName: "db/b1.dump", Offset: 0, Length: 5})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("read = %q, want %q", data, "hello")
	}

	if err := m.delete("db/b1.dump"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "db", "b1.dump")); !os.IsNotExist(err) {
		t.Fatalf("file still exists after delete: %v", err)
	}
}

// A symlink already present in the restore directory (not in the archive)
// must not redirect an archive entry's write outside it.
func TestRestoreProjectRejectsPreexistingSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	withBackupsRoot(t, root)
	writeMaliciousArchive(t, root, "evil5", []tar.Header{
		{Name: "link/pwned", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	})

	outside := t.TempDir()
	dest := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "link")); err != nil {
		t.Fatal(err)
	}

	err := restoreProject(protocol.BackupCommandPayload{FileName: "projects/evil5.tar.gz", RestoreDir: dest})
	if err == nil {
		t.Fatal("restoreProject followed a pre-existing symlink out of the restore directory")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "pwned")); statErr == nil {
		t.Fatal("file was written outside the restore directory")
	}
}
