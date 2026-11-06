package detect

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// serverFuncs are package-level functions that start a server, by import path.
var serverFuncs = map[string][]string{
	"net/http":                    {"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"},
	"net":                         {"Listen", "ListenTCP", "ListenUnix"},
	"github.com/valyala/fasthttp": {"ListenAndServe", "ListenAndServeTLS", "ListenAndServeUNIX", "Serve"},
}

// serverMethods are methods that start a server on a value from a known framework, by import path prefix.
// Without type information the receiver is unknown, so the framework must be imported (see methodStartsServer).
var serverMethods = map[string][]string{
	"net/http":                    {"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"},
	"github.com/gin-gonic/gin":    {"Run", "RunTLS", "RunUnix", "RunListener", "RunFd"},
	"github.com/labstack/echo":    {"Start", "StartTLS", "StartAutoTLS", "StartServer", "StartH2CServer"},
	"github.com/gofiber/fiber":    {"Listen", "ListenTLS", "Listener", "ListenMutualTLS"},
	"google.golang.org/grpc":      {"Serve"},
	"github.com/valyala/fasthttp": {"ListenAndServe", "ListenAndServeTLS", "Serve"},
	"github.com/cloudwego/hertz":  {"Spin", "Run"},
}

// Short method names like Run are common on non-server types (workers, CLIs), so they only count when
// the calling file itself imports the framework. Distinctive names count when any reached package imports it.
var genericMethods = map[string]bool{"Run": true, "Start": true, "Serve": true, "Listen": true, "Spin": true, "Listener": true}

type pkgInfo struct {
	dir     string
	name    string
	files   []*fileInfo
	funcs   map[string][]*funcInfo // package-level functions by name; init can repeat
	methods map[string][]*funcInfo // methods by name, any receiver
}

type fileInfo struct {
	path    string
	ast     *ast.File
	imports map[string]string // local name -> import path
}

type funcInfo struct {
	pkg  *pkgInfo
	file *fileInfo
	decl *ast.FuncDecl
}

type analyzer struct {
	fsys    fs.FS
	modPath string
	fset    *token.FileSet
	pkgs    map[string]*pkgInfo
}

func newAnalyzer(fsys fs.FS, modPath string) *analyzer {
	return &analyzer{fsys: fsys, modPath: modPath, fset: token.NewFileSet(), pkgs: map[string]*pkgInfo{}}
}

// kind follows calls from main() and init() through the module's own packages and reports web if any reached
// code starts a server. Third-party code isn't in the repo, so known server starts are matched by import path.
func (a *analyzer) kind(dir string) (string, string, error) {
	main, err := a.load(dir)
	if err != nil {
		return "", "", err
	}
	w := walker{a: a, reached: map[*pkgInfo]bool{}, seen: map[*funcInfo]bool{}, methodNames: map[string]bool{}}
	w.reach(main)
	w.enqueue(main.funcs["main"]...)
	for len(w.queue) > 0 {
		fn := w.queue[0]
		w.queue = w.queue[1:]
		if hit := w.visit(fn); hit != "" {
			return Web, hit, nil
		}
	}
	return Worker, "no server start found from main()", nil
}

type walker struct {
	a           *analyzer
	queue       []*funcInfo
	reached     map[*pkgInfo]bool
	seen        map[*funcInfo]bool
	methodNames map[string]bool
}

func (w *walker) enqueue(fns ...*funcInfo) {
	for _, fn := range fns {
		if !w.seen[fn] {
			w.seen[fn] = true
			w.queue = append(w.queue, fn)
		}
	}
}

func (w *walker) reach(p *pkgInfo) {
	if w.reached[p] {
		return
	}
	w.reached[p] = true
	w.enqueue(p.funcs["init"]...)
	for name := range w.methodNames {
		w.enqueue(p.methods[name]...)
	}
}

func (w *walker) callMethod(name string) {
	if w.methodNames[name] {
		return
	}
	w.methodNames[name] = true
	for p := range w.reached {
		w.enqueue(p.methods[name]...)
	}
}

// visit scans one function body; it returns a reason when the body starts a server.
func (w *walker) visit(fn *funcInfo) string {
	if fn.decl.Body == nil {
		return ""
	}
	var hit string
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if hit != "" {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || w.isImport(fn.file, sel.X) {
				return true
			}
			if framework, ok := w.methodStartsServer(fn.file, sel.Sel.Name); ok {
				hit = w.reason(n.Pos(), fmt.Sprintf("%s, %s", types.ExprString(sel), framework))
				return false
			}
			w.callMethod(sel.Sel.Name)
		case *ast.SelectorExpr:
			id, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			imp, ok := fn.file.imports[id.Name]
			if !ok {
				return true
			}
			if funcStartsServer(imp, n.Sel.Name) {
				hit = w.reason(n.Pos(), types.ExprString(n))
				return false
			}
			if dir, local := w.a.localDir(imp); local {
				p, err := w.a.load(dir)
				if err == nil {
					w.reach(p)
					w.enqueue(p.funcs[n.Sel.Name]...)
				}
			}
			return false
		case *ast.Ident:
			w.enqueue(fn.pkg.funcs[n.Name]...)
		}
		return true
	})
	return hit
}

func (w *walker) isImport(f *fileInfo, x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = f.imports[id.Name]
	return ok
}

func (w *walker) reason(pos token.Pos, call string) string {
	p := w.a.fset.Position(pos)
	return fmt.Sprintf("starts a server in %s:%d (%s)", p.Filename, p.Line, call)
}

func funcStartsServer(importPath, name string) bool {
	for _, fn := range serverFuncs[importPath] {
		if fn == name {
			return true
		}
	}
	return false
}

func (w *walker) methodStartsServer(f *fileInfo, name string) (string, bool) {
	prefixes := make([]string, 0, len(serverMethods))
	for prefix := range serverMethods {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		if !contains(serverMethods[prefix], name) {
			continue
		}
		if importsPrefix(f, prefix) {
			return frameworkName(prefix), true
		}
		if genericMethods[name] {
			continue
		}
		for p := range w.reached {
			for _, other := range p.files {
				if importsPrefix(other, prefix) {
					return frameworkName(prefix), true
				}
			}
		}
	}
	return "", false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func importsPrefix(f *fileInfo, prefix string) bool {
	for _, imp := range f.imports {
		if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
			return true
		}
	}
	return false
}

func frameworkName(prefix string) string {
	return path.Base(prefix)
}

func (a *analyzer) localDir(importPath string) (string, bool) {
	if importPath == a.modPath {
		return ".", true
	}
	rest, ok := strings.CutPrefix(importPath, a.modPath+"/")
	return rest, ok
}

// load parses the buildable files of a package in the module, once.
func (a *analyzer) load(dir string) (*pkgInfo, error) {
	if p, ok := a.pkgs[dir]; ok {
		return p, nil
	}
	p := &pkgInfo{dir: dir, funcs: map[string][]*funcInfo{}, methods: map[string][]*funcInfo{}}
	a.pkgs[dir] = p
	srcs, err := buildableFiles(a.fsys, dir)
	if err != nil {
		return p, err
	}
	for _, src := range srcs {
		f, err := parser.ParseFile(a.fset, src.path, src.src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		p.name = f.Name.Name
		fi := &fileInfo{path: src.path, ast: f, imports: map[string]string{}}
		p.files = append(p.files, fi)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			info := &funcInfo{pkg: p, file: fi, decl: fd}
			if fd.Recv == nil {
				p.funcs[fd.Name.Name] = append(p.funcs[fd.Name.Name], info)
			} else {
				p.methods[fd.Name.Name] = append(p.methods[fd.Name.Name], info)
			}
		}
	}
	for _, fi := range p.files {
		for _, spec := range fi.ast.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			name := a.importName(imp)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name == "_" || name == "." {
				continue
			}
			fi.imports[name] = imp
		}
	}
	return p, nil
}

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// importName is the name a file uses for an unaliased import: the real package name for module packages,
// and Go's usual conventions for everything else (echo/v4 -> echo, nats.go -> nats, yaml.v3 -> yaml, go-redis -> redis).
func (a *analyzer) importName(imp string) string {
	if dir, local := a.localDir(imp); local {
		if p, err := a.load(dir); err == nil && p.name != "" {
			return p.name
		}
	}
	parts := strings.Split(imp, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && majorSuffix.MatchString(last) {
		last = parts[len(parts)-2]
	}
	last, _, _ = strings.Cut(last, ".")
	last = strings.TrimPrefix(last, "go-")
	return strings.ReplaceAll(last, "-", "")
}
