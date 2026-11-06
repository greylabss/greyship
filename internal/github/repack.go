package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"greyship.tech/internal/pack"
)

const (
	maxFiles  = 50_000
	maxUnpack = 512 << 20
)

// Repack turns GitHub's archive of a repo into the source tarball the CLI would have uploaded: GitHub's top folder
// is dropped, and internal/pack applies the same rules (.gitignore, .greyshipignore, never .git, .env files or
// symlinks), so a deploy from GitHub builds exactly what grey deploy would.
func Repack(archive []byte, w io.Writer) (pack.Result, error) {
	dir, err := os.MkdirTemp("", "greyship-repo-*")
	if err != nil {
		return pack.Result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := extract(archive, dir); err != nil {
		return pack.Result{}, err
	}
	return pack.Write(dir, w)
}

// extract writes GitHub's tar.gz under dir without its first path element. Every name is checked: the archive
// comes from a user's repository.
func extract(archive []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return errors.New("GitHub's archive isn't a .tar.gz")
	}
	tr := tar.NewReader(gz)
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("GitHub's archive is damaged")
		}
		_, rest, ok := strings.Cut(h.Name, "/")
		if !ok || rest == "" {
			continue // the top folder itself, or the pax header GitHub adds
		}
		name := filepath.Clean(filepath.FromSlash(rest))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the repository has a file outside its folder: %s", rest)
		}
		target := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		case tar.TypeReg:
		default:
			continue // symlinks and the rest are never part of a deploy
		}
		if files++; files > maxFiles {
			return errors.New("the repository has more than 50,000 files")
		}
		if total += h.Size; total > maxUnpack {
			return errors.New("the repository unpacks to more than 512 MB")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) //nolint:gosec // checked to be inside dir
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// archiveFile returns one file at the top of GitHub's archive (under its top folder), at most 64KB.
func archiveFile(archive []byte, name string) ([]byte, bool) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, false
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, false
		}
		_, rest, _ := strings.Cut(h.Name, "/")
		if rest == name && h.Typeflag == tar.TypeReg && h.Size <= 64<<10 {
			body, err := io.ReadAll(io.LimitReader(tr, h.Size))
			return body, err == nil
		}
	}
}
