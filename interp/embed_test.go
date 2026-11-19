package interp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func evalEmbed(t *testing.T, fsys fstest.MapFS, path string) (string, error) {
	t.Helper()
	return evalEmbedOpt(t, fsys, path, false)
}

func evalEmbedFS(t *testing.T, fsys fstest.MapFS, path string) (string, error) {
	t.Helper()
	return evalEmbedOpt(t, fsys, path, true)
}

func evalEmbedOpt(t *testing.T, fsys fstest.MapFS, path string, useStdlib bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, Stdout: &out, Stderr: &out})
	if useStdlib {
		if err := i.Use(stdlib.Symbols); err != nil {
			t.Fatal(err)
		}
	}
	_, err := i.EvalPath(path)
	return out.String(), err
}

func TestEmbedStringAndByteSlice(t *testing.T) {
	// hello.txt lives only in the virtual source filesystem, next to the
	// Go file. A different file at the filesystem root must not be read.
	fsys := fstest.MapFS{
		"hello.txt":     &fstest.MapFile{Data: []byte("WRONG")},
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("hello")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

//go:embed hello.txt
var s string

//go:embed hello.txt
var b []byte

var plain string
var plainN int
var n = len(s)

func init() {
	if s != "hello" || string(b) != "hello" || n != 5 || len(b) != 5 {
		panic("embed missing before init")
	}
}

func main() {
	println(s)
	println(string(b))
	println(n)
	println(len(plain))
	println(plainN)
	s = s + "!"
	b[0] = 'H'
	println(s)
	println(string(b))
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "hello\nhello\n5\n0\n0\nhello!\nHello\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmbedInVarGroup(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("hello")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

var (
	//go:embed hello.txt
	s string
	n = len(s)
	z int

	//go:embed hello.txt
	b []byte
)

func init() {
	if s != "hello" || n != 5 || string(b) != "hello" || z != 0 {
		panic("group embed cleared")
	}
}

func main() {
	println(s)
	println(n)
	println(z)
	println(string(b))
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "hello\n5\n0\nhello\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmbedAboveVarGroup(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("grouped")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

//go:embed hello.txt
var (
	s string
)

func main() {
	println(s)
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "grouped\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedPackageDir(t *testing.T) {
	// EvalPath on a directory goes through importSrc rather than Compile/Execute.
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("fromdir")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

//go:embed hello.txt
var s string

func main() {
	println(s)
}
`)},
	}
	got, err := evalEmbed(t, fsys, "./pkg")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fromdir\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedPatterns(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/data/only.txt": &fstest.MapFile{Data: []byte("solo")},
		"pkg/data/.hidden":  &fstest.MapFile{Data: []byte("nope")},
		"pkg/my file.txt":   &fstest.MapFile{Data: []byte("spaced")},
		"pkg/bin.dat":       &fstest.MapFile{Data: []byte{0, 255, 'A'}},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

//go:embed data/*.txt
var one string

//go:embed "my file.txt"
var spaced string

//go:embed bin.dat
var raw []byte

//go:embed data
var fromDir string

func main() {
	println(one)
	println(spaced)
	println(len(raw))
	println(raw[0])
	println(raw[1])
	println(string(raw[2:]))
	println(fromDir)
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "solo\nspaced\n3\n0\n255\nA\nsolo\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmbedUint8AndAlias(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("alias")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

type S = string
type B = []byte

var (
	//go:embed hello.txt
	s S
	//go:embed hello.txt
	b B
	//go:embed hello.txt
	u []uint8
)

func main() {
	println(s)
	println(string(b))
	println(string(u))
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alias\nalias\nalias\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fs   fstest.MapFS
		want string
	}{
		{
			name: "missing",
			src: `package main
//go:embed missing.txt
var s string
func main() {}
`,
			want: "no matching files found",
		},
		{
			name: "multiple",
			src: `package main
//go:embed data/*.txt
var s string
func main() {}
`,
			fs: fstest.MapFS{
				"pkg/data/a.txt": &fstest.MapFile{Data: []byte("a")},
				"pkg/data/b.txt": &fstest.MapFile{Data: []byte("b")},
			},
			want: "exactly one",
		},
		{
			name: "parent",
			src: `package main
//go:embed ../hello.txt
var s string
func main() {}
`,
			fs:   fstest.MapFS{"hello.txt": &fstest.MapFile{Data: []byte("secret")}},
			want: "invalid pattern syntax",
		},
		{
			name: "type",
			src: `package main
//go:embed hello.txt
var n int
func main() {}
`,
			fs:   fstest.MapFS{"pkg/hello.txt": &fstest.MapFile{Data: []byte("x")}},
			want: "cannot apply to var of type",
		},
		{
			name: "defined type",
			src: `package main
type S string
//go:embed hello.txt
var s S
func main() {}
`,
			fs:   fstest.MapFS{"pkg/hello.txt": &fstest.MapFile{Data: []byte("x")}},
			want: "cannot apply to var of type",
		},
		{
			name: "initializer",
			src: `package main
//go:embed hello.txt
var s string = "x"
func main() {}
`,
			want: "initializer",
		},
		{
			name: "multiple vars",
			src: `package main
//go:embed hello.txt
var a, b string
func main() {}
`,
			want: "multiple vars",
		},
		{
			name: "group multiple",
			src: `package main
//go:embed hello.txt
var (
	a string
	b string
)
func main() {}
`,
			want: "multiple vars",
		},
		{
			name: "local",
			src: `package main
func main() {
	//go:embed hello.txt
	var s string
	println(s)
}
`,
			want: "package-level",
		},
		{
			name: "const",
			src: `package main
//go:embed hello.txt
const s = "x"
func main() {}
`,
			want: "package-level",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := tc.fs
			if fsys == nil {
				fsys = fstest.MapFS{}
			}
			fsys["pkg/main.go"] = &fstest.MapFile{Data: []byte(tc.src)}
			_, err := evalEmbed(t, fsys, "pkg/main.go")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestEmbedIgnoresNonDirective(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("hello")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

// go:embed hello.txt
var s string

func main() {
	println(len(s))
}
`)},
	}
	got, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0\n" {
		t.Fatalf("got %q, want zero length", got)
	}
}

func TestEmbedFS(t *testing.T) {
	// hello.txt at the filesystem root is a decoy. Patterns resolve next to the
	// Go file, and embed.FS is read through io/fs rather than the host disk.
	fsys := fstest.MapFS{
		"hello.txt":          &fstest.MapFile{Data: []byte("WRONG")},
		"pkg/hello.txt":      &fstest.MapFile{Data: []byte("hello")},
		"pkg/data/only.txt":  &fstest.MapFile{Data: []byte("solo")},
		"pkg/data/sub/n.txt": &fstest.MapFile{Data: []byte("nest")},
		"pkg/data/.hidden":   &fstest.MapFile{Data: []byte("nope")},
		"pkg/data/_skip.txt": &fstest.MapFile{Data: []byte("nope")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"io/fs"
)

//go:embed hello.txt
var single embed.FS

var (
	//go:embed hello.txt data
	grouped embed.FS
)

//go:embed hello.txt
var (
	above embed.FS
)

func mustRead(f embed.FS, name string) string {
	b, err := fs.ReadFile(f, name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var early = mustRead(single, "hello.txt")

func init() {
	if early != "hello" || mustRead(above, "hello.txt") != "hello" {
		panic("embed.FS missing before init")
	}
	if mustRead(grouped, "hello.txt") != "hello" || mustRead(grouped, "data/only.txt") != "solo" || mustRead(grouped, "data/sub/n.txt") != "nest" {
		panic("tree")
	}
	if _, err := fs.ReadFile(grouped, "data/.hidden"); err == nil {
		panic("hidden")
	}
	if _, err := fs.ReadFile(grouped, "data/_skip.txt"); err == nil {
		panic("skip")
	}
	if _, err := fs.ReadFile(grouped, "WRONG"); err == nil {
		panic("root")
	}
}

func main() {
	println(early)
	println(mustRead(single, "hello.txt"))
	println(mustRead(above, "hello.txt"))

	ents, err := fs.ReadDir(grouped, ".")
	if err != nil {
		panic(err)
	}
	for i := 0; i < len(ents); i++ {
		println(ents[i].Name())
		if ents[i].IsDir() {
			println("dir")
		} else {
			println("file")
		}
	}
	ents, err = fs.ReadDir(grouped, "data")
	if err != nil {
		panic(err)
	}
	for i := 0; i < len(ents); i++ {
		println(ents[i].Name())
		if ents[i].IsDir() {
			println("dir")
		} else {
			println("file")
		}
	}
	println(mustRead(grouped, "data/sub/n.txt"))

	b, err := fs.ReadFile(single, "hello.txt")
	if err != nil {
		panic(err)
	}
	b[0] = 'H'
	println(mustRead(single, "hello.txt"))

	f, err := single.Open("hello.txt")
	if err != nil {
		panic(err)
	}
	buf := make([]byte, 8)
	n, err := f.Read(buf)
	if n != 5 {
		panic(n)
	}
	println(string(buf[:n]))
	f.Close()
}
`)},
	}
	got, err := evalEmbedFS(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"hello",
		"hello",
		"hello",
		"data",
		"dir",
		"hello.txt",
		"file",
		"only.txt",
		"file",
		"sub",
		"dir",
		"nest",
		"hello",
		"hello",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmbedFSAlias(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("alias")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"io/fs"
)

type E = embed.FS

//go:embed hello.txt
var files E

func main() {
	b, err := fs.ReadFile(files, "hello.txt")
	if err != nil {
		panic(err)
	}
	println(string(b))
	ents, err := fs.ReadDir(files, ".")
	if err != nil || len(ents) != 1 {
		panic(err)
	}
	println(ents[0].Name())
}
`)},
	}
	got, err := evalEmbedFS(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alias\nhello.txt\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedFSErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fs   fstest.MapFS
		want string
	}{
		{
			name: "defined type",
			src: `package main
import "embed"
type E embed.FS
//go:embed hello.txt
var files E
func main() {}
`,
			fs:   fstest.MapFS{"pkg/hello.txt": &fstest.MapFile{Data: []byte("x")}},
			want: "cannot apply to var of type",
		},
		{
			name: "missing",
			src: `package main
import "embed"
//go:embed missing.txt
var files embed.FS
func main() {}
`,
			want: "no matching files found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := tc.fs
			if fsys == nil {
				fsys = fstest.MapFS{}
			}
			fsys["pkg/main.go"] = &fstest.MapFile{Data: []byte(tc.src)}
			_, err := evalEmbedFS(t, fsys, "pkg/main.go")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestEmbedRealSourceFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package main
//go:embed hello.txt
var s string
func main() { println(s) }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	i := interp.New(interp.Options{Stdout: &out, Stderr: &out})
	if _, err := i.EvalPath(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "disk\n" {
		t.Fatalf("got %q", out.String())
	}
}
