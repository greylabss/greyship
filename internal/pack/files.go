package pack

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// The files bundle (greyship.toml's files setting): repo files shipped next to every service's binary, at the
// same relative paths. It is packed from files that already passed the source rules (ignore files, never .git,
// .env files or symlinks), so it can never hold anything the source tarball wouldn't.
const (
	MaxBundleBytes = 50 << 20 // before compression
	MaxBundleFiles = 5000
)

// BundleEntry is how much one files entry brought in, for the deploy log.
type BundleEntry struct {
	Path  string
	Files int
	Bytes int64
}

type BundleResult struct {
	Files   int
	Bytes   int64
	Entries []BundleEntry
}

// SelectError is a files entry that matched nothing, so a typo fails the deploy instead of shipping without it.
type SelectError struct{ Entry string }

func (e *SelectError) Error() string {
	return fmt.Sprintf("greyship.toml lists %q in files, but there's no such file or folder (or it's ignored)", e.Entry)
}

// Select returns the names (slash paths, from the allowed files) that the entries cover: a file by its path, a
// folder by everything under it.
func Select(allowed []string, entries []string) ([]string, []BundleEntry, error) {
	seen := map[string]bool{}
	var out []string
	var counts []BundleEntry
	for _, e := range entries {
		dir := strings.TrimSuffix(e, "/")
		count := BundleEntry{Path: e}
		for _, name := range allowed {
			if name != dir && !strings.HasPrefix(name, dir+"/") {
				continue
			}
			count.Files++
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		if count.Files == 0 {
			return nil, nil, &SelectError{Entry: e}
		}
		counts = append(counts, count)
	}
	sort.Strings(out)
	return out, counts, nil
}

// Bundle writes the named files of fsys to w as a .tar.gz with zeroed timestamps and owners, in name order, so
// the same files always give the same bytes and sha256.
func Bundle(fsys fs.FS, names []string, entries []BundleEntry, w io.Writer) (BundleResult, error) {
	res := BundleResult{Files: len(names), Entries: entries}
	if len(names) > MaxBundleFiles {
		return res, fmt.Errorf("files lists %d files; the limit is %d", len(names), MaxBundleFiles)
	}
	sizes := map[string]int64{}
	for _, name := range names {
		info, err := fs.Stat(fsys, name)
		if err != nil {
			return res, fmt.Errorf("read %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return res, fmt.Errorf("%s isn't a regular file", name)
		}
		sizes[name] = info.Size()
		res.Bytes += info.Size()
	}
	if res.Bytes > MaxBundleBytes {
		return res, fmt.Errorf("files adds up to %.1f MB; the limit is %d MB", float64(res.Bytes)/(1<<20), MaxBundleBytes>>20)
	}
	for i := range res.Entries {
		for _, name := range names {
			dir := strings.TrimSuffix(res.Entries[i].Path, "/")
			if name == dir || strings.HasPrefix(name, dir+"/") {
				res.Entries[i].Bytes += sizes[name]
			}
		}
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		if err := addBundleFile(tw, fsys, name, sizes[name]); err != nil {
			return res, err
		}
	}
	if err := tw.Close(); err != nil {
		return res, err
	}
	return res, gz.Close()
}

func addBundleFile(tw *tar.Writer, fsys fs.FS, name string, size int64) error {
	f, err := fsys.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: path.Clean(name), Mode: 0o444, Size: size, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("pack %s: %w", name, err)
	}
	if _, err := io.CopyN(tw, f, size); err != nil {
		return fmt.Errorf("pack %s: %w", name, err)
	}
	return nil
}

// Summary is the deploy log line for a bundle, e.g. "templates/ (12 files), scripts/install.sh · 340 KB".
func (r BundleResult) Summary() string {
	parts := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		if strings.HasSuffix(e.Path, "/") || e.Files > 1 {
			unit := "files"
			if e.Files == 1 {
				unit = "file"
			}
			parts = append(parts, fmt.Sprintf("%s (%d %s)", e.Path, e.Files, unit))
			continue
		}
		parts = append(parts, e.Path)
	}
	return strings.Join(parts, ", ") + " · " + humanSize(r.Bytes)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ErrUnsafeEntry is a files entry that could point outside the repo.
var ErrUnsafeEntry = errors.New("unsafe files entry")

// CheckEntry validates one files entry: a relative path inside the repo, no wildcards.
func CheckEntry(e string) error {
	clean := path.Clean(strings.TrimSuffix(e, "/"))
	switch {
	case e == "" || strings.TrimSpace(e) != e:
		return fmt.Errorf("%w: %q is empty or has spaces around it", ErrUnsafeEntry, e)
	case strings.Contains(e, `\`):
		return fmt.Errorf("%w: %q: use / between folders", ErrUnsafeEntry, e)
	case path.IsAbs(e) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../"):
		return fmt.Errorf("%w: %q must be a path inside the repo, like templates/ or scripts/install.sh", ErrUnsafeEntry, e)
	case strings.ContainsAny(e, "*?["):
		return fmt.Errorf("%w: %q: list files or folders, without wildcards", ErrUnsafeEntry, e)
	case clean != strings.TrimSuffix(e, "/"):
		return fmt.Errorf("%w: %q: write it as %s", ErrUnsafeEntry, e, clean)
	}
	return nil
}

// BundleDir bundles the entries from a project folder on disk (grey deploy), with the source rules applied.
func BundleDir(root string, entries []string, w io.Writer) (BundleResult, error) {
	files, _, err := List(root)
	if err != nil {
		return BundleResult{}, err
	}
	allowed := make([]string, len(files))
	for i, f := range files {
		allowed[i] = f.Path
	}
	names, counts, err := Select(allowed, entries)
	if err != nil {
		return BundleResult{}, err
	}
	return Bundle(os.DirFS(root), names, counts, w)
}

// BundleFS bundles the entries from an unpacked source tarball (builds on Greyship), whose files already passed
// the source rules when it was packed.
func BundleFS(fsys fs.FS, entries []string, w io.Writer) (BundleResult, error) {
	var allowed []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			allowed = append(allowed, p)
		}
		return nil
	})
	if err != nil {
		return BundleResult{}, err
	}
	names, counts, err := Select(allowed, entries)
	if err != nil {
		return BundleResult{}, err
	}
	return Bundle(fsys, names, counts, w)
}
