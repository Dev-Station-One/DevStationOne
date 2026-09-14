package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devstationone/internal/protocol"
)

func openTestFS(t *testing.T) *FS {
	t.Helper()
	f, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestClean(t *testing.T) {
	// (input, want, wantErr)
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"a/b", "a/b", false},
		{"./a//b/", "a/b", false},
		{".", ".", false},
		{"", "", true},
		{"/etc/passwd", "", true},
		{"..", "", true},
		{"../x", "", true},
		{"a/../../x", "", true},
		{"a/./../b", "b", false},
	}
	for _, c := range cases {
		got, err := Clean(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Clean(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListStatRead(t *testing.T) {
	f := openTestFS(t)
	if err := f.Write("hello.txt", []byte("hello world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Mkdir("sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	ents, err := f.List(".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := map[string]protocol.FileInfo{}
	for _, e := range ents {
		byName[e.Name] = e
	}
	if len(ents) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(ents))
	}
	if byName["hello.txt"].Type != "file" || byName["hello.txt"].Size != 11 {
		t.Errorf("hello.txt info = %+v", byName["hello.txt"])
	}
	if byName["sub"].Type != "dir" {
		t.Errorf("sub type = %q, want dir", byName["sub"].Type)
	}
	if !strings.HasPrefix(byName["hello.txt"].Mode, "-rw-r--r--") {
		t.Errorf("mode = %q", byName["hello.txt"].Mode)
	}

	st, err := f.Stat("hello.txt")
	if err != nil || st.Size != 11 || st.Type != "file" {
		t.Errorf("Stat = %+v, err %v", st, err)
	}

	data, err := f.Read("hello.txt")
	if err != nil || string(data) != "hello world" {
		t.Errorf("Read = %q, err %v", data, err)
	}
	if _, err := f.Read("sub"); err == nil {
		t.Error("Read on directory should fail")
	}
	if _, err := f.Read("missing"); err == nil {
		t.Error("Read on missing file should fail")
	}
}

func TestCreateMkdirExclusive(t *testing.T) {
	f := openTestFS(t)
	if err := f.Create("new.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.Create("new.txt"); err == nil {
		t.Error("Create on existing file should fail")
	}
	data, err := f.Read("new.txt")
	if err != nil || len(data) != 0 {
		t.Errorf("created file content = %q, err %v", data, err)
	}
	if err := f.Mkdir("d"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := f.Mkdir("d"); err == nil {
		t.Error("Mkdir on existing dir should fail")
	}
}

func TestWriteOverwrites(t *testing.T) {
	f := openTestFS(t)
	if err := f.Write("f.txt", []byte("one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Write("f.txt", []byte("twelve")); err != nil {
		t.Fatalf("Write again: %v", err)
	}
	data, err := f.Read("f.txt")
	if err != nil || string(data) != "twelve" {
		t.Errorf("Read = %q, err %v", data, err)
	}
}

func TestRenameMoveDelete(t *testing.T) {
	f := openTestFS(t)
	f.Write("a.txt", []byte("aa"))
	f.Mkdir("d")
	f.Write("d/inner.txt", []byte("ii"))

	if err := f.Rename("a.txt", "b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := f.Stat("a.txt"); err == nil {
		t.Error("a.txt should be gone")
	}
	if err := f.Move("b.txt", "d/moved.txt"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := f.Delete("d"); err != nil {
		t.Fatalf("Delete dir: %v", err)
	}
	if _, err := f.Stat("d/moved.txt"); err == nil {
		t.Error("d/moved.txt should be gone after Delete of d")
	}
	if err := f.Delete("."); err == nil {
		t.Error("Delete of the project root should fail")
	}
}

func TestCopyFileAndTree(t *testing.T) {
	f := openTestFS(t)
	f.Write("src.txt", []byte("content"))
	f.Mkdir("tree")
	f.Write("tree/x.txt", []byte("x"))
	f.Mkdir("tree/nested")
	f.Write("tree/nested/y.txt", []byte("y"))

	if err := f.Copy("src.txt", "dst.txt"); err != nil {
		t.Fatalf("Copy file: %v", err)
	}
	if data, err := f.Read("dst.txt"); err != nil || string(data) != "content" {
		t.Errorf("copy content = %q, err %v", data, err)
	}
	if err := f.Copy("src.txt", "dst.txt"); err == nil {
		t.Error("Copy onto existing target should fail")
	}

	if err := f.Copy("tree", "tree-copy"); err != nil {
		t.Fatalf("Copy dir: %v", err)
	}
	for _, p := range []string{"tree-copy", "tree-copy/x.txt", "tree-copy/nested/y.txt"} {
		if _, err := f.Stat(p); err != nil {
			t.Errorf("Copy tree missing %s: %v", p, err)
		}
	}
	if data, err := f.Read("tree-copy/nested/y.txt"); err != nil || string(data) != "y" {
		t.Errorf("nested copy content = %q, err %v", data, err)
	}
}

func TestReadCap(t *testing.T) {
	f := openTestFS(t)
	// Sparse file: oversized on paper without costing disk.
	big, err := f.root.OpenFile("big.bin", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("create big file: %v", err)
	}
	if err := big.Truncate(int64(protocol.MaxFileBytes) + 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	big.Close()

	if _, err := f.Read("big.bin"); err == nil || !strings.Contains(err.Error(), "transfer limit") {
		t.Errorf("Read oversized file err = %v, want transfer-limit error", err)
	}
	if err := f.Write("big2.bin", make([]byte, int64(protocol.MaxFileBytes)+1)); err == nil {
		t.Error("Write oversized payload should fail")
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	f, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	// A symlink planted inside the root but pointing outside it.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := f.Read("escape"); err == nil {
		t.Error("Read through escaping symlink should fail")
	}
	if _, err := f.Stat("escape"); err == nil {
		t.Error("Stat through escaping symlink should fail")
	}
	// List still shows the symlink entry (as a symlink), which is honest.
	ents, err := f.List(".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, e := range ents {
		if e.Name == "escape" {
			found = e.Type == "symlink"
		}
	}
	if !found {
		t.Error("escaping symlink should still be listed as type symlink")
	}
}

func TestSymlinkInsideRootFollowed(t *testing.T) {
	f := openTestFS(t)
	f.Write("real.txt", []byte("real"))
	if err := os.Symlink("real.txt", filepath.Join(f.root.Name(), "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	data, err := f.Read("link.txt")
	if err != nil || string(data) != "real" {
		t.Errorf("Read through in-root symlink = %q, err %v", data, err)
	}
}

func TestSearch(t *testing.T) {
	f := openTestFS(t)
	f.Mkdir("dir")
	f.Mkdir("dir/deep")
	seeds := map[string]string{
		"a.log":              "x",
		"b.txt":              "x",
		"dir/alpha.txt":      "x",
		"dir/deep/beta.log":  "x",
		"dir/deep/other.log": "x",
	}
	for p, data := range seeds {
		if err := f.Write(p, []byte(data)); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	matches, err := f.Search(".", ".log", false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := map[string]bool{"a.log": true, "dir/deep/beta.log": true, "dir/deep/other.log": true}
	if len(matches) != len(want) {
		t.Fatalf("Search matches = %v, want %v", matches, want)
	}
	for _, m := range matches {
		if !want[m] {
			t.Errorf("unexpected match %q", m)
		}
	}

	sub, err := f.Search("dir", "beta", false)
	if err != nil || len(sub) != 1 || sub[0] != "dir/deep/beta.log" {
		t.Errorf("Search subtree = %v, err %v", sub, err)
	}
	if _, err := f.Search(".", "", false); err == nil {
		t.Error("Search with empty query should fail")
	}
}

// TestSearchVisitedCapTruncates reproduces a bug where hitting
// searchMaxVisited returned an error and discarded every match found so
// far (surfaced as a hard failure, e.g. 502 from the browse/search and
// files/search HTTP handlers, on any real-world directory with more than
// searchMaxVisited entries). Search must instead truncate the walk and
// return the matches already collected, with no error.
func TestSearchVisitedCapTruncates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < searchMaxVisited+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatalf("seed file %d: %v", i, err)
		}
	}
	f, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	matches, err := f.Search(".", "target", true)
	if err != nil {
		t.Fatalf("Search should truncate, not error: %v", err)
	}
	if len(matches) != 1 || matches[0] != "target" {
		t.Errorf("Search over-cap matches = %v, want [target]", matches)
	}
}

// TestSearchFindsShallowMatchPastExpensiveSibling reproduces a bug where
// Search recursed fully into each subdirectory before checking later
// siblings, so one large sibling subtree (e.g. node_modules) could exhaust
// searchMaxVisited before the walk ever reached a shallow match next to it
// — dropping it regardless of directory read order. Search must scan every
// directory's own children before descending into any of them, so a
// top-level match is always found.
func TestSearchFindsShallowMatchPastExpensiveSibling(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(dir, "decoy")
	if err := os.Mkdir(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < searchMaxVisited+1; i++ {
		if err := os.WriteFile(filepath.Join(decoy, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatalf("seed decoy file %d: %v", i, err)
		}
	}
	f, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	matches, err := f.Search(".", "target", true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0] != "target" {
		t.Errorf("Search matches = %v, want [target] (must not be starved by the decoy subtree)", matches)
	}
}

func TestSearchDirsOnly(t *testing.T) {
	f := openTestFS(t)
	f.Mkdir("target")
	f.Mkdir("other")
	f.Write("target.txt", []byte("x"))

	matches, err := f.Search(".", "target", true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0] != "target" {
		t.Errorf("Search dirsOnly matches = %v, want [target]", matches)
	}
}
