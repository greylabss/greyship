package detect

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// fileReaders are calls that open a path at runtime, by import path. Only instances ship nothing but the binary
// (and greyship.toml's files), so a repo path read this way works locally and fails on Greyship.
var fileReaders = map[string]map[string]bool{
	"os":            {"Open": true, "ReadFile": true, "ReadDir": true, "Stat": true, "DirFS": true},
	"html/template": {"ParseFiles": true, "ParseGlob": true},
	"text/template": {"ParseFiles": true, "ParseGlob": true},
	"net/http":      {"Dir": true},
}

// runtimeReads warns about string-literal repo paths the code reads while running that greyship.toml's files
// don't ship. It is a hint, not a rule: it never fails a deploy, and it only sees literal paths.
func runtimeReads(fsys fs.FS, files []string) ([]string, error) {
	type hit struct {
		at, read, entry string
	}
	var hits []hit
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return nil // detection reports broken files elsewhere; a hint must not fail the deploy
		}
		imports := map[string]string{}
		for _, im := range f.Imports {
			ip, _ := strconv.Unquote(im.Path.Value)
			local := path.Base(ip)
			if im.Name != nil {
				local = im.Name.Name
			}
			imports[local] = ip
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || !fileReaders[imports[pkg.Name]][sel.Sel.Name] {
				return true
			}
			args := call.Args
			if sel.Sel.Name != "ParseFiles" && len(args) > 1 {
				args = args[:1]
			}
			for _, a := range args {
				lit, ok := a.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				raw, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				read, entry, ok := repoPath(fsys, raw, sel.Sel.Name == "ParseGlob")
				if !ok || shipped(read, files) {
					continue
				}
				hits = append(hits, hit{at: fmt.Sprintf("%s:%d", p, fset.Position(lit.Pos()).Line), read: read, entry: entry})
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.read] {
			continue
		}
		seen[h.read] = true
		out = append(out, fmt.Sprintf("%s reads %s while running, but only the binary is shipped; add it to greyship.toml (files = [%q]) or use //go:embed", h.at, h.read, h.entry))
	}
	return out, nil
}

// repoPath reports whether raw names something in the repo (a file, a folder, or a glob that matches), and the
// files entry that would ship it.
func repoPath(fsys fs.FS, raw string, glob bool) (read, entry string, ok bool) {
	if raw == "" || path.IsAbs(raw) || strings.Contains(raw, `\`) {
		return "", "", false
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", false
	}
	if glob {
		matches, err := fs.Glob(fsys, clean)
		if err != nil || len(matches) == 0 {
			return "", "", false
		}
		dir := path.Dir(clean)
		if dir == "." {
			return clean, matches[0], true
		}
		return clean, dir + "/", true
	}
	info, err := fs.Stat(fsys, clean)
	if err != nil {
		return "", "", false
	}
	if info.IsDir() {
		return clean + "/", clean + "/", true
	}
	return clean, clean, true
}

// shipped reports whether a files entry covers the path read.
func shipped(read string, files []string) bool {
	r := strings.TrimSuffix(read, "/")
	for _, f := range files {
		e := strings.TrimSuffix(f, "/")
		if r == e || strings.HasPrefix(r, e+"/") {
			return true
		}
	}
	return false
}
