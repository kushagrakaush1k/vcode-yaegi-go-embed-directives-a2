package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// attachEmbed records //go:embed patterns placed immediately above a
// package-level var, either on the declaration or on a spec inside a group.
func (n *node) attachEmbed(parent ast.Node, spec *ast.ValueSpec) error {
	gd, ok := parent.(*ast.GenDecl)
	if !ok || spec == nil {
		return nil
	}
	specPatterns, err := embedDirectivePatterns(spec.Doc)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}
	declPatterns, err := embedDirectivePatterns(gd.Doc)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}
	if len(specPatterns) == 0 && len(declPatterns) == 0 {
		return nil
	}
	if gd.Tok != token.VAR || n.anc == nil || n.anc.kind != varDecl || n.anc.anc == nil || n.anc.anc.kind != fileStmt {
		return n.cfgErrorf("go:embed only allowed on a package-level var")
	}
	if len(declPatterns) > 0 && countValueNames(gd) != 1 {
		return n.cfgErrorf("go:embed cannot apply to multiple vars")
	}
	if len(spec.Names) != 1 {
		return n.cfgErrorf("go:embed cannot apply to multiple vars")
	}
	if spec.Values != nil {
		return n.cfgErrorf("go:embed cannot apply to var with initializer")
	}
	patterns := make([]string, 0, len(declPatterns)+len(specPatterns))
	patterns = append(patterns, declPatterns...)
	patterns = append(patterns, specPatterns...)
	n.embedPatterns = patterns
	return nil
}

func countValueNames(gd *ast.GenDecl) int {
	n := 0
	for _, s := range gd.Specs {
		vs, ok := s.(*ast.ValueSpec)
		if !ok {
			continue
		}
		n += len(vs.Names)
	}
	return n
}

// embedDirectivePatterns returns patterns from //go:embed lines in g.
// A line is a directive only when it begins with "//go:embed" followed by
// whitespace or the end of the comment. Other comments are ignored.
func embedDirectivePatterns(g *ast.CommentGroup) ([]string, error) {
	if g == nil {
		return nil, nil
	}
	var patterns []string
	for _, c := range g.List {
		raw := c.Text
		const prefix = "//go:embed"
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		rest := raw[len(prefix):]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return nil, fmt.Errorf("go:embed: missing pattern")
		}
		pats, err := splitEmbedPatterns(rest)
		if err != nil {
			return nil, err
		}
		if len(pats) == 0 {
			return nil, fmt.Errorf("go:embed: missing pattern")
		}
		patterns = append(patterns, pats...)
	}
	return patterns, nil
}

// splitEmbedPatterns splits a //go:embed argument list. Patterns are
// separated by spaces. A pattern that contains spaces may be quoted.
func splitEmbedPatterns(s string) ([]string, error) {
	var out []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		if s[0] == '"' || s[0] == '`' {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, fmt.Errorf("go:embed: invalid pattern syntax")
			}
			u, err := strconv.Unquote(q)
			if err != nil {
				return nil, fmt.Errorf("go:embed: invalid pattern syntax")
			}
			out = append(out, u)
			s = s[len(q):]
			continue
		}
		if s[0] == '\'' {
			return nil, fmt.Errorf("go:embed: invalid pattern syntax")
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i])
		s = s[i:]
	}
	return out, nil
}

func isEmbedString(t *itype) bool {
	return t != nil && t.cat == stringT && t.name == "string" && t.path == ""
}

func isEmbedBytes(t *itype) bool {
	if t == nil || t.cat != sliceT || t.name != "" || t.path != "" || t.val == nil {
		return false
	}
	v := t.val
	return v.cat == uint8T && v.path == "" && (v.name == "uint8" || v.name == "byte")
}

// setEmbedValue reads the single file selected by n.embedPatterns from the
// interpreter source filesystem and stores it on n. The directory is the
// directory of the Go file that contains the directive.
func (interp *Interpreter) setEmbedValue(n *node) error {
	if n.typ == nil || (!isEmbedString(n.typ) && !isEmbedBytes(n.typ)) {
		name := "<unknown>"
		if n.typ != nil {
			name = n.typ.id()
		}
		return n.cfgErrorf("go:embed cannot apply to var of type %s", name)
	}
	dir := path.Dir(filepath.ToSlash(interp.fset.Position(n.pos).Filename))
	data, err := interp.readEmbed(dir, n.embedPatterns)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}
	v := reflect.New(n.typ.frameType()).Elem()
	if isEmbedString(n.typ) {
		v.SetString(string(data))
	} else {
		buf := make([]byte, len(data))
		copy(buf, data)
		v.SetBytes(buf)
	}
	n.embedVal = v
	n.hasEmbed = true
	return nil
}

// readEmbed resolves patterns relative to dir through the interpreter
// source filesystem. string and []byte require exactly one file.
func (interp *Interpreter) readEmbed(dir string, patterns []string) ([]byte, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, pattern := range patterns {
		matches, err := matchEmbed(interp.opt.filesystem, dir, pattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			files = append(files, m)
		}
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("go:embed: matched %d files, want exactly one", len(files))
	}
	return fs.ReadFile(interp.opt.filesystem, files[0])
}

// applyEmbeds copies //go:embed values into global frame slots at index >= from.
// Those slots were just zeroed; this puts the file contents in place before
// any statement, including the usual var initialization, runs.
func (interp *Interpreter) applyEmbeds(from int) {
	if interp.frame == nil || interp.universe == nil {
		return
	}
	var walk func(*scope)
	walk = func(sc *scope) {
		if sc == nil {
			return
		}
		for _, sym := range sc.sym {
			if sym == nil || !sym.hasEmbed || sym.index < from || sym.index >= len(interp.frame.data) {
				continue
			}
			interp.frame.data[sym.index] = sym.embed
		}
		for _, ch := range sc.child {
			walk(ch)
		}
	}
	walk(interp.universe)
}

func validEmbedPattern(pattern string) error {
	if pattern == "" || strings.Contains(pattern, `\`) {
		return fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}
	if strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") {
		return fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}
	body := pattern
	if strings.HasPrefix(body, "./") {
		body = body[2:]
	}
	if body == "" || strings.HasPrefix(body, "/") {
		return fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}
	for _, elem := range strings.Split(body, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}
		if _, err := path.Match(elem, "x"); err != nil {
			return fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}
	}
	return nil
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func embedElemMatch(elem, name string) (bool, error) {
	ok, err := path.Match(elem, name)
	if err != nil || !ok || name == "" {
		return false, err
	}
	// Names starting with '.' or '_' are excluded from wildcard matches
	// unless the pattern element itself starts with that character.
	if (name[0] == '.' || name[0] == '_') && hasMeta(elem) && elem[0] != '.' && elem[0] != '_' {
		return false, nil
	}
	return true, nil
}

func joinEmbed(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return path.Join(dir, name)
}

func cleanEmbedPath(p string) string {
	if p == "" || p == "." {
		return "."
	}
	return p
}

// matchEmbed returns the files in fsys selected by pattern. pattern is
// relative to dir. A literal directory contributes the regular files under
// it, skipping names that start with '.' or '_'.
func matchEmbed(fsys fs.FS, dir, pattern string) ([]string, error) {
	if fsys == nil {
		return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
	}
	if err := validEmbedPattern(pattern); err != nil {
		return nil, err
	}
	body := pattern
	if strings.HasPrefix(body, "./") {
		body = body[2:]
	}
	elems := strings.Split(body, "/")
	var matches []string
	var walk func(base string, depth int) error
	walk = func(base string, depth int) error {
		if depth == len(elems) {
			full := cleanEmbedPath(base)
			info, err := fs.Stat(fsys, full)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if info.IsDir() {
				return embedDirFiles(fsys, full, &matches)
			}
			if info.Mode().IsRegular() {
				matches = append(matches, full)
			}
			return nil
		}
		elem := elems[depth]
		if !hasMeta(elem) {
			return walk(joinEmbed(base, elem), depth+1)
		}
		readFrom := base
		if readFrom == "" || readFrom == "." {
			readFrom = "."
		}
		entries, err := fs.ReadDir(fsys, readFrom)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			ok, err := embedElemMatch(elem, e.Name())
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := walk(joinEmbed(base, e.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	start := dir
	if start == "." {
		start = ""
	}
	if err := walk(start, 0); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
	}
	sort.Strings(matches)
	return matches, nil
}

// embedDirFiles appends regular files under root, skipping names that
// start with '.' or '_'. root itself is included only as the walk start.
func embedDirFiles(fsys fs.FS, root string, matches *[]string) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if name == "" || name[0] == '.' || name[0] == '_' {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		*matches = append(*matches, p)
		return nil
	})
}
