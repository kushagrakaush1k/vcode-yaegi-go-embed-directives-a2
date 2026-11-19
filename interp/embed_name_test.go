package interp_test

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func TestEmbedTypeName(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/hello.txt": &fstest.MapFile{Data: []byte("hello")},
		"pkg/main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"reflect"
)

//go:embed hello.txt
var files embed.FS

func main() {
	fmt.Printf("T=%T\n", files)
	fmt.Printf("str=%s\n", reflect.TypeOf(files))
	fmt.Printf("name=%s\n", reflect.TypeOf(files).Name())
	fmt.Printf("pkg=%s\n", reflect.TypeOf(files).PkgPath())
	b, err := files.ReadFile("hello.txt")
	if err != nil {
		panic(err)
	}
	fmt.Printf("data=%s\n", b)
}
`)},
	}
	var out bytes.Buffer
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, Stdout: &out, Stderr: &out})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("pkg/main.go"); err != nil {
		t.Fatal(err)
	}
	want := "T=embed.FS\nstr=embed.FS\nname=FS\npkg=embed\ndata=hello\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}
