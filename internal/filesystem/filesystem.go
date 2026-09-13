// Package filesystem implements the Development Runtime's filesystem
// capability: list, stat, read, write, create, mkdir, rename, copy, move,
// delete, and search, all strictly contained in one authorized root
// directory (the Project's working directory).
//
// Containment rests on os.Root (docs/SECURITY.md): every operation resolves
// its path against the open Root, which refuses ".." traversal and refuses
// to follow symlinks that escape the root — even ones planted inside it.
// Clean additionally rejects absolute paths and ".." before they reach the
// Root, so the Control Server can pre-validate with the same rule.
package filesystem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"devstation/internal/protocol"
)

// searchCaps bound a "search" walk.
// ponytail: fixed caps, no config knob unless a real project needs one.
const (
	searchMaxDepth   = 12
	searchMaxMatches = 200
	searchMaxVisited = 20000
)

// Clean validates and normalizes a root-relative path. It rejects empty and
// absolute paths and anything that would escape the root. The result "."
// refers to the root itself.
func Clean(p string) (string, error) {
	if p == "" {
		return "", errors.New("path is required")
	}
	if path.IsAbs(p) {
		return "", fmt.Errorf("path %q must be relative to the project root", p)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes the project root", p)
	}
	return c, nil
}

// FS performs containment-checked filesystem operations on one root
// directory. All path arguments are relative to it.
type FS struct {
	root *os.Root
}

// Open opens rootPath as the containment root for subsequent operations.
func Open(rootPath string) (*FS, error) {
	r, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open root %q: %w", rootPath, err)
	}
	return &FS{root: r}, nil
}

// Close releases the root handle.
func (f *FS) Close() error { return f.root.Close() }

// List returns the entries of directory p.
func (f *FS) List(p string) ([]protocol.FileInfo, error) {
	ents, err := f.readDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.FileInfo, 0, len(ents))
	for _, e := range ents {
		inf, err := e.Info()
		if err != nil {
			// The entry exists (ReadDir saw it); its stat just failed, e.g.
			// a raced-away file. Skip rather than fail the whole listing.
			continue
		}
		out = append(out, toInfo(e.Name(), inf))
	}
	return out, nil
}

// Stat returns the entry at p, following in-root symlinks.
func (f *FS) Stat(p string) (protocol.FileInfo, error) {
	c, err := Clean(p)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	inf, err := f.root.Stat(c)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	return toInfo(path.Base(c), inf), nil
}

// Read returns the full contents of the file at p, capped at
// protocol.MaxFileBytes.
func (f *FS) Read(p string) ([]byte, error) {
	c, err := Clean(p)
	if err != nil {
		return nil, err
	}
	inf, err := f.root.Stat(c)
	if err != nil {
		return nil, err
	}
	if inf.IsDir() {
		return nil, fmt.Errorf("%q is a directory", p)
	}
	if inf.Size() > protocol.MaxFileBytes {
		return nil, tooLarge(p, inf.Size())
	}
	data, err := f.root.ReadFile(c)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > protocol.MaxFileBytes {
		// The file grew between Stat and Read.
		return nil, tooLarge(p, int64(len(data)))
	}
	return data, nil
}

// Write replaces the file at p with data, creating it (0644) when absent.
func (f *FS) Write(p string, data []byte) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}
	if int64(len(data)) > protocol.MaxFileBytes {
		return tooLarge(p, int64(len(data)))
	}
	return f.root.WriteFile(c, data, 0o644)
}

// Create creates an empty file at p, failing if it already exists.
func (f *FS) Create(p string) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}
	fd, err := f.root.OpenFile(c, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return fd.Close()
}

// Mkdir creates directory p, failing if it already exists.
func (f *FS) Mkdir(p string) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}
	return f.root.Mkdir(c, 0o755)
}

// Rename renames p to to, replacing to if it exists (POSIX semantics).
func (f *FS) Rename(p, to string) error {
	c, cTo, err := cleanPaths(p, to)
	if err != nil {
		return err
	}
	return f.root.Rename(c, cTo)
}

// Copy copies the file or directory tree at p to to, failing if to exists.
func (f *FS) Copy(p, to string) error {
	c, cTo, err := cleanPaths(p, to)
	if err != nil {
		return err
	}
	if cTo == "." {
		return errors.New("copy destination cannot be the project root")
	}
	src, err := f.root.Open(c)
	if err != nil {
		return err
	}
	defer src.Close()
	sinf, err := src.Stat()
	if err != nil {
		return err
	}
	if sinf.IsDir() {
		return copyDir(f, c, cTo, sinf)
	}
	return copyFile(f, c, cTo, sinf.Mode().Perm())
}

// Move renames p to to (same filesystem — the operation is contained in one
// root).
func (f *FS) Move(p, to string) error { return f.Rename(p, to) }

// Delete removes the file or directory tree at p.
func (f *FS) Delete(p string) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}
	if c == "." {
		return errors.New("cannot delete the project root")
	}
	return f.root.RemoveAll(c)
}

// Search returns root-relative paths of entries whose name contains query
// (case-insensitive), walking the subtree rooted at p. Bounded by the
// searchCaps constants.
func (f *FS) Search(p, query string) ([]string, error) {
	c, err := Clean(p)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query is required")
	}
	q := strings.ToLower(query)
	var matches []string
	visited := 0

	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > searchMaxDepth {
			return nil
		}
		ents, err := f.readDir(dir)
		if err != nil {
			return nil // unreadable subtree: skip it, keep searching elsewhere
		}
		for _, e := range ents {
			visited++
			if visited > searchMaxVisited {
				return errors.New("search exceeded entry limit")
			}
			name := e.Name()
			full := path.Join(dir, name)
			if dir == "." {
				full = name
			}
			if strings.Contains(strings.ToLower(name), q) {
				matches = append(matches, full)
				if len(matches) >= searchMaxMatches {
					return nil
				}
			}
			if e.IsDir() {
				if err := walk(full, depth+1); err != nil {
					return err
				}
				if len(matches) >= searchMaxMatches {
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(c, 0); err != nil {
		return matches, err
	}
	return matches, nil
}

// --- helpers ---

// readDir lists directory p through a Root-scoped handle. os.Root itself has
// no ReadDir, so the listing goes through root.Open(p).
func (f *FS) readDir(p string) ([]os.DirEntry, error) {
	c, err := Clean(p)
	if err != nil {
		return nil, err
	}
	dir, err := f.root.Open(c)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	ents, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	return ents, nil
}

func toInfo(name string, inf os.FileInfo) protocol.FileInfo {
	t := "file"
	mode := inf.Mode()
	switch {
	case inf.IsDir():
		t = "dir"
	case mode&os.ModeSymlink != 0:
		t = "symlink"
	case !mode.IsRegular():
		t = "other"
	}
	return protocol.FileInfo{
		Name:    name,
		Type:    t,
		Size:    inf.Size(),
		Mode:    mode.String(),
		ModTime: inf.ModTime().UTC(),
	}
}

func cleanPaths(p, to string) (string, string, error) {
	c, err := Clean(p)
	if err != nil {
		return "", "", err
	}
	cTo, err := Clean(to)
	if err != nil {
		return "", "", err
	}
	return c, cTo, nil
}

func copyFile(f *FS, from, to string, perm os.FileMode) error {
	src, err := f.root.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := f.root.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func copyDir(f *FS, from, to string, sinf os.FileInfo) error {
	if err := f.root.Mkdir(to, sinf.Mode().Perm()); err != nil {
		return err
	}
	ents, err := f.readDir(from)
	if err != nil {
		return err
	}
	for _, e := range ents {
		eFrom := path.Join(from, e.Name())
		eTo := path.Join(to, e.Name())
		inf, err := e.Info()
		if err != nil {
			return err
		}
		if inf.IsDir() {
			if err := copyDir(f, eFrom, eTo, inf); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(f, eFrom, eTo, inf.Mode().Perm()); err != nil {
			// A raced-away or unreadable entry must not abort the copy of
			// everything else.
			if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func tooLarge(p string, size int64) error {
	return fmt.Errorf("%q is %d bytes, exceeding the %d MB transfer limit", p, size, protocol.MaxFileBytes>>20)
}
