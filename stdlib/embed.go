package stdlib

import (
	"reflect"

	"github.com/traefik/yaegi/stdlib/embedfs"
)

func init() {
	// embed.FS is filled by the interpreter from //go:embed. The standard
	// library type has no constructor, so this read-only filesystem stands in
	// for it and implements fs.FS, fs.ReadFileFS, and fs.ReadDirFS.
	Symbols["embed/embed"] = map[string]reflect.Value{
		"FS": reflect.ValueOf((*embedfs.FS)(nil)),
	}
}
