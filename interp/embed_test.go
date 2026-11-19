package interp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
)

func evalEmbed(t *testing.T, fsys fstest.MapFS, path string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, Stdout: &out, Stderr: &out})
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
