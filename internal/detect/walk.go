package detect

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"strings"
)

// The target the builder compiles for; files that wouldn't be part of that build are ignored.
const (
	targetOS   = "linux"
	targetArch = "amd64"
)

type mainDir struct {
	dir  string // slash path relative to the root, "." for the root
	skip string // why it isn't deployed; empty when it is a candidate
}

// findMains walks the module and returns every folder with a buildable package main, in walk order.
func findMains(fsys fs.FS, modPath string) ([]mainDir, error) {
	skipReason := map[string]string{}
	generated := map[string]string{} // tool dir -> file that runs it via go:generate
	var mains []mainDir

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p != "." && (name == "vendor" || name == "node_modules" || name == ".git") {
			return fs.SkipDir
		}
		reason := skipReason[path.Dir(p)]
		if reason == "" && p != "." {
			reason = dirSkipReason(fsys, p, name)
		}
		skipReason[p] = reason

		files, err := buildableFiles(fsys, p)
		if err != nil {
			return err
		}
		isMain := false
		for _, f := range files {
			if f.pkg == "main" {
				isMain = true
			}
			for _, target := range goRunTargets(f.src, p, modPath) {
				if _, ok := generated[target]; !ok {
					generated[target] = f.path
				}
			}
		}
		if isMain {
			mains = append(mains, mainDir{dir: p, skip: reason})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	for i, m := range mains {
		if by, ok := generated[m.dir]; ok && m.skip == "" {
			mains[i].skip = "only run by go:generate in " + by
		}
	}
	return mains, nil
}

func dirSkipReason(fsys fs.FS, p, name string) string {
	switch {
	case name == "testdata":
		return "testdata folders are skipped"
	case name == "examples" || name == "example":
		return "examples are skipped"
	case strings.HasPrefix(name, "_") || strings.HasPrefix(name, "."):
		return "folders starting with _ or . are skipped"
	}
	if _, err := fs.Stat(fsys, path.Join(p, "go.mod")); err == nil {
		return "separate Go module (its own go.mod); multi-module projects come later"
	}
	return ""
}

type srcFile struct {
	path string
	pkg  string
	src  []byte
}

// buildableFiles returns the non-test .go files in dir that a linux/amd64, CGO-off build would compile.
func buildableFiles(fsys fs.FS, dir string) ([]srcFile, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []srcFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || !matchesTarget(name) {
			continue
		}
		p := path.Join(dir, name)
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, src, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			continue // a file that doesn't parse can't be built either; the build step reports it
		}
		if !satisfiesConstraints(f.Comments, f.Package) {
			continue
		}
		out = append(out, srcFile{path: p, pkg: f.Name.Name, src: src})
	}
	return out, nil
}

var knownOS = map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true}

var knownArch = map[string]bool{"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true, "mips": true, "mips64": true, "mips64le": true, "mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true}

// matchesTarget applies Go's filename rules: name_GOOS.go, name_GOARCH.go, name_GOOS_GOARCH.go.
func matchesTarget(name string) bool {
	parts := strings.Split(strings.TrimSuffix(name, ".go"), "_")
	if len(parts) < 2 {
		return true
	}
	last := parts[len(parts)-1]
	if knownArch[last] {
		if last != targetArch {
			return false
		}
		if len(parts) >= 3 && knownOS[parts[len(parts)-2]] {
			return parts[len(parts)-2] == targetOS
		}
		return true
	}
	if knownOS[last] {
		return last == targetOS
	}
	return true
}

// satisfiesConstraints evaluates //go:build (or legacy // +build) lines above the package clause.
func satisfiesConstraints(groups []*ast.CommentGroup, pkgPos token.Pos) bool {
	var plus []constraint.Expr
	for _, g := range groups {
		if g.End() >= pkgPos {
			break
		}
		for _, c := range g.List {
			switch {
			case constraint.IsGoBuild(c.Text):
				expr, err := constraint.Parse(c.Text)
				if err != nil {
					return false
				}
				return expr.Eval(buildTag)
			case constraint.IsPlusBuild(c.Text):
				if expr, err := constraint.Parse(c.Text); err == nil {
					plus = append(plus, expr)
				}
			}
		}
	}
	for _, expr := range plus {
		if !expr.Eval(buildTag) {
			return false
		}
	}
	return true
}

// buildTag reports which tags hold for the builder: linux/amd64 with the gc toolchain and CGO off.
// "ignore", "tools" and other custom tags are false, so files guarded by them drop out.
func buildTag(tag string) bool {
	switch tag {
	case targetOS, targetArch, "unix", "gc":
		return true
	}
	return strings.HasPrefix(tag, "go1.")
}

// goRunTargets finds folders a file runs with //go:generate go run, resolved to repo-relative dirs.
func goRunTargets(src []byte, dir, modPath string) []string {
	if !bytes.Contains(src, []byte("//go:generate")) {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "//go:generate")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 3 || fields[0] != "go" || fields[1] != "run" {
			continue
		}
		if target := localTarget(firstNonFlag(fields[2:]), dir, modPath); target != "" {
			out = append(out, target)
		}
	}
	return out
}

func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// localTarget resolves a go run argument (./x, ../x, ./x/main.go or a module import path) to a repo-relative dir.
func localTarget(arg, dir, modPath string) string {
	relative := strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || arg == "."
	if strings.HasSuffix(arg, ".go") {
		arg = path.Dir(arg)
	}
	switch {
	case relative:
		return clean(path.Join(dir, arg))
	case arg == modPath:
		return "."
	case strings.HasPrefix(arg, modPath+"/"):
		return strings.TrimPrefix(arg, modPath+"/")
	}
	return ""
}
