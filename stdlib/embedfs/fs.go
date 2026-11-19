// Package embedfs builds a standard embed.FS from files chosen by //go:embed.
package embedfs

import (
	"embed"
	"path"
	"sort"
	"strings"
	"unsafe"
)

// embedFile matches the unexported file struct in package embed.
// embed.FS is one pointer to a slice of these values, sorted by directory
// then base name, with directories stored as names that end in '/'.
type embedFile struct {
	name string
	data string
	hash [16]byte
}

// Build returns an embed.FS containing the regular files in files.
// Names are slash-separated paths relative to the embedding Go file.
// Contents are copied. Parent directories are added so Open and ReadDir work.
func Build(files map[string][]byte) embed.FS {
	if len(files) == 0 {
		return embed.FS{}
	}
	dirs := map[string]struct{}{}
	list := make([]embedFile, 0, len(files))
	for name, data := range files {
		list = append(list, embedFile{name: name, data: string(data)})
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := dirs[dir]; ok {
				break
			}
			dirs[dir] = struct{}{}
		}
	}
	for dir := range dirs {
		list = append(list, embedFile{name: dir + "/"})
	}
	sort.Slice(list, func(i, j int) bool {
		return embedLess(list[i].name, list[j].name)
	})
	return freeze(list)
}

// embedLess reports the order embed.FS binary search expects:
// compare the directory, then the last element.
func embedLess(a, b string) bool {
	adir, aelem := splitEmbed(a)
	bdir, belem := splitEmbed(b)
	if adir != bdir {
		return adir < bdir
	}
	return aelem < belem
}

func splitEmbed(name string) (dir, elem string) {
	name = strings.TrimSuffix(name, "/")
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return ".", name
	}
	return name[:i], name[i+1:]
}

func freeze(list []embedFile) embed.FS {
	files := list
	var out embed.FS
	type header struct {
		files *[]embedFile
	}
	(*header)(unsafe.Pointer(&out)).files = &files
	return out
}
