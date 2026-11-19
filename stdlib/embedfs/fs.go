// Package embedfs is the read-only filesystem stored in an embed.FS variable.
package embedfs

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// FS is the concrete type registered as embed.FS.
// Copies share one file tree, and the tree does not change after New.
type FS struct {
	files *fileTree
}

type fileTree struct {
	files map[string]string
}

// New returns a filesystem containing the given regular files.
// Names are slash-separated paths relative to the embedding Go file.
// File contents are copied.
func New(files map[string][]byte) FS {
	tree := &fileTree{files: make(map[string]string, len(files))}
	for name, data := range files {
		tree.files[name] = string(data)
	}
	return FS{files: tree}
}

func (f FS) fileMap() map[string]string {
	if f.files == nil {
		return nil
	}
	return f.files.files
}

// Open opens the named file or directory for reading.
func (f FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	switch kind, data := f.lookup(name); kind {
	case kindFile:
		return &openFile{path: name, data: data}, nil
	case kindDir:
		ents, err := f.entries(name)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &openDir{path: name, entries: ents}, nil
	default:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
}

// ReadFile returns a copy of the named file's contents.
func (f FS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	switch kind, data := f.lookup(name); kind {
	case kindFile:
		return []byte(data), nil
	case kindDir:
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	default:
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
}

// ReadDir reads the named directory and returns its entries sorted by name.
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	ents, err := f.entries(name)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	return ents, nil
}

const (
	kindMissing = iota
	kindFile
	kindDir
)

func (f FS) lookup(name string) (int, string) {
	if name != "." {
		if data, ok := f.fileMap()[name]; ok {
			return kindFile, data
		}
	}
	if f.isDir(name) {
		return kindDir, ""
	}
	return kindMissing, ""
}

func (f FS) isDir(name string) bool {
	if name == "." {
		return true
	}
	prefix := name + "/"
	for p := range f.fileMap() {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func (f FS) entries(dir string) ([]fs.DirEntry, error) {
	kind, _ := f.lookup(dir)
	if kind == kindMissing {
		return nil, fs.ErrNotExist
	}
	if kind != kindDir {
		return nil, errors.New("not a directory")
	}
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	kids := map[string]dirEntry{}
	for p, data := range f.fileMap() {
		rest := p
		if prefix != "" {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			rest = p[len(prefix):]
		}
		elem, _, more := strings.Cut(rest, "/")
		if elem == "" {
			continue
		}
		e := kids[elem]
		e.name = elem
		if more {
			e.dir = true
			e.size = 0
		} else if !e.dir {
			e.size = int64(len(data))
		}
		kids[elem] = e
	}
	names := make([]string, 0, len(kids))
	for name := range kids {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, len(names))
	for i, name := range names {
		out[i] = kids[name]
	}
	return out, nil
}

type dirEntry struct {
	name string
	dir  bool
	size int64
}

func (d dirEntry) Name() string { return d.name }
func (d dirEntry) IsDir() bool  { return d.dir }
func (d dirEntry) Type() fs.FileMode {
	if d.dir {
		return fs.ModeDir
	}
	return 0
}
func (d dirEntry) Info() (fs.FileInfo, error) { return fileInfo{d}, nil }

type fileInfo struct{ dirEntry }

func (i fileInfo) Size() int64 { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) Sys() any           { return nil }

type openFile struct {
	path   string
	data   string
	offset int64
}

func (f *openFile) Stat() (fs.FileInfo, error) {
	return fileInfo{dirEntry{name: path.Base(f.path), size: int64(len(f.data))}}, nil
}

func (f *openFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *openFile) Close() error { return nil }

type openDir struct {
	path    string
	entries []fs.DirEntry
	offset  int
}

func (d *openDir) Stat() (fs.FileInfo, error) {
	name := d.path
	if name != "." {
		name = path.Base(name)
	}
	return fileInfo{dirEntry{name: name, dir: true}}, nil
}

func (d *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errors.New("is a directory")}
}

func (d *openDir) Close() error { return nil }

func (d *openDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.offset >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 {
		ents := d.entries[d.offset:]
		d.offset = len(d.entries)
		return ents, nil
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	ents := d.entries[d.offset:end]
	d.offset = end
	return ents, nil
}

var (
	_ fs.FS          = FS{}
	_ fs.ReadFileFS  = FS{}
	_ fs.ReadDirFS   = FS{}
	_ fs.File        = (*openFile)(nil)
	_ fs.ReadDirFile = (*openDir)(nil)
	_ fs.DirEntry    = dirEntry{}
	_ fs.FileInfo    = fileInfo{}
)
