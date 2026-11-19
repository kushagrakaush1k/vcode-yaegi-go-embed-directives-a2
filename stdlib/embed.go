package stdlib

import (
	"embed"
	"reflect"
)

func init() {
	// embed.FS values are filled by the interpreter from //go:embed.
	Symbols["embed/embed"] = map[string]reflect.Value{
		"FS": reflect.ValueOf((*embed.FS)(nil)),
	}
}
