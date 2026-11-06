package pack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheckEntry(t *testing.T) {
	for _, ok := range []string{"templates/", "templates", "scripts/install.sh", "a/b/c/"} {
		if err := CheckEntry(ok); err != nil {
			t.Errorf("CheckEntry(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", " x", "/etc/passwd", "../secrets", "a/../../b", ".", "*.html", "templates/*", `a\b`, "./templates", "a//b"} {
		if err := CheckEntry(bad); !errors.Is(err, ErrUnsafeEntry) {
			t.Errorf("CheckEntry(%q) accepted it", bad)
		}
	}
}

func bundleNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[h.Name] = string(body)
	}
}

func TestBundleFS(t *testing.T) {
	src := fstest.MapFS{
		"go.mod":               {Data: []byte("module x")},
		"templates/home.html":  {Data: []byte("<h1>hi</h1>")},
		"templates/a/b.html":   {Data: []byte("b")},
		"templatesX/other.txt": {Data: []byte("not under templates/")},
		"scripts/install.sh":   {Data: []byte("#!/bin/sh")},
		"scripts/other.sh":     {Data: []byte("no")},
	}
	var a, b bytes.Buffer
	res, err := BundleFS(src, []string{"templates/", "scripts/install.sh"}, &a)
	if err != nil {
		t.Fatal(err)
	}
	got := bundleNames(t, a.Bytes())
	if len(got) != 3 || got["templates/home.html"] != "<h1>hi</h1>" || got["scripts/install.sh"] != "#!/bin/sh" || got["templates/a/b.html"] != "b" {
		t.Fatalf("bundle = %v", got)
	}
	if s := res.Summary(); s != "templates/ (2 files), scripts/install.sh · 21 B" {
		t.Fatalf("summary = %q", s)
	}
	if _, err := BundleFS(src, []string{"templates/", "scripts/install.sh"}, &b); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("the same files must give the same bytes")
	}
	var missing *SelectError
	if _, err := BundleFS(src, []string{"templates/", "static/"}, io.Discard); !errors.As(err, &missing) || missing.Entry != "static/" {
		t.Fatalf("missing entry = %v", err)
	}
}

func TestBundleDirKeepsSourceRules(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "conf/local.yaml\n")
	write("conf/app.yaml", "port: 1")
	write("conf/local.yaml", "ignored")
	write("conf/.env", "SECRET=1")
	_ = os.Symlink("/etc/passwd", filepath.Join(root, "conf", "link"))
	var out bytes.Buffer
	if _, err := BundleDir(root, []string{"conf/"}, &out); err != nil {
		t.Fatal(err)
	}
	got := bundleNames(t, out.Bytes())
	if len(got) != 1 || got["conf/app.yaml"] != "port: 1" {
		t.Fatalf("bundle = %v; ignored files, .env files and symlinks must stay out", got)
	}
	if _, err := BundleDir(root, []string{"conf/.env"}, io.Discard); err == nil {
		t.Fatal("an .env file was bundled by naming it")
	}
}

func TestBundleLimits(t *testing.T) {
	src := fstest.MapFS{"big.bin": {Data: make([]byte, MaxBundleBytes+1)}}
	if _, err := BundleFS(src, []string{"big.bin"}, io.Discard); err == nil || !strings.Contains(err.Error(), "limit is 50 MB") {
		t.Fatalf("err = %v", err)
	}
}
