// Package pack turns a project folder into the source tarball grey deploy uploads (spec F-20).
// The same files always give the same bytes, so the sha256 identifies the source.
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
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxSize is the largest compressed source the API accepts (spec §6).
const MaxSize = 100 << 20

// ErrTooLarge means the compressed tarball passed MaxSize. Largest lists the biggest files to point at.
type ErrTooLarge struct{ Largest []File }

func (e *ErrTooLarge) Error() string {
	return fmt.Sprintf("source is larger than %d MB compressed", MaxSize>>20)
}

type File struct {
	Path string // slash-separated, relative to the root
	Size int64
	mode fs.FileMode
}

type Result struct {
	Files    int
	Bytes    int64    // before compression
	Symlinks []string // not uploaded
}

// List returns the files that would be uploaded, sorted by path.
// Ignore files are honoured, and some paths are never uploaded whatever they say:
// .git, node_modules, and env files, which hold secrets and reach Greyship only through env sync (spec F-40).
func List(root string) ([]File, []string, error) {
	var files []File
	var symlinks []string
	var rules []rule
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rules = append(rules, readRules(root, "")...)
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || ignored(rules, rel, true) {
				return filepath.SkipDir
			}
			rules = append(rules, readRules(p, rel)...)
			return nil
		}
		if isEnvFile(name) || ignored(rules, rel, false) {
			return nil
		}
		// Symlinks could point outside the project, so they are listed and left out.
		if d.Type()&fs.ModeSymlink != 0 {
			symlinks = append(symlinks, rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, File{Path: rel, Size: info.Size(), mode: info.Mode()})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, symlinks, nil
}

func isEnvFile(name string) bool { return name == ".env" || strings.HasPrefix(name, ".env.") }

func readRules(dir, base string) []rule {
	var rules []rule
	for _, name := range []string{".gitignore", ".greyshipignore"} {
		f, err := os.Open(filepath.Join(dir, name)) //nolint:gosec // inside the folder being packed
		if err != nil {
			continue
		}
		rules = append(rules, parseIgnore(f, base)...)
		_ = f.Close()
	}
	return rules
}

// Write packs root into w as a .tar.gz. Timestamps and owners are zeroed so the output depends only on content.
func Write(root string, w io.Writer) (Result, error) {
	files, symlinks, err := List(root)
	if err != nil {
		return Result{}, err
	}
	res := Result{Files: len(files), Symlinks: symlinks}
	counted := &limitWriter{w: w, max: MaxSize}
	gz := gzip.NewWriter(counted)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := addFile(tw, root, f); err != nil {
			if errors.Is(err, errLimit) {
				return res, &ErrTooLarge{Largest: largest(files, 5)}
			}
			return res, err
		}
		res.Bytes += f.Size
	}
	if err := tw.Close(); err != nil {
		return res, closeErr(err, files)
	}
	if err := gz.Close(); err != nil {
		return res, closeErr(err, files)
	}
	return res, nil
}

func addFile(tw *tar.Writer, root string, f File) error {
	src, err := os.Open(filepath.Join(root, filepath.FromSlash(f.Path))) //nolint:gosec // listed from inside root
	if err != nil {
		return fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer func() { _ = src.Close() }()
	mode := int64(0o644)
	if f.mode&0o111 != 0 {
		mode = 0o755
	}
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: path.Clean(f.Path), Mode: mode, Size: f.Size, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("pack %s: %w", f.Path, err)
	}
	// A file that grows while packing fails cleanly instead of writing a bad entry.
	if _, err := io.CopyN(tw, src, f.Size); err != nil {
		return fmt.Errorf("pack %s: %w", f.Path, err)
	}
	return nil
}

func closeErr(err error, files []File) error {
	if errors.Is(err, errLimit) {
		return &ErrTooLarge{Largest: largest(files, 5)}
	}
	return fmt.Errorf("finish tarball: %w", err)
}

func largest(files []File, n int) []File {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

var errLimit = errors.New("size limit reached")

type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n+int64(len(p)) > l.max {
		return 0, errLimit
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}
