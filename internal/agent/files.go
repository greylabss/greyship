package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"greyship.tech/internal/pack"
)

// addFiles copies a files bundle into the image under filesDir, read-only and owned by the app's user. The
// bundle was made by Greyship, but it holds the user's files, so every name is checked again and only regular
// files are taken.
func addFiles(tw *tar.Writer, bundle []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return errors.New("the bundle isn't a .tar.gz")
	}
	dirs := map[string]bool{}
	addDir := func(dir string) error {
		var parts []string
		for _, p := range strings.Split(dir, "/") {
			parts = append(parts, p)
			d := strings.Join(parts, "/")
			if dirs[d] {
				continue
			}
			dirs[d] = true
			hdr := &tar.Header{Typeflag: tar.TypeDir, Name: d + "/", Mode: 0o555, Uid: 65534, Gid: 65534, ModTime: time.Unix(0, 0)}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		}
		return nil
	}
	if err := addDir(filesDir); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var count int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("the bundle is damaged")
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg || path.IsAbs(name) || name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("the bundle has an unsafe entry %q", h.Name)
		}
		if count++; count > pack.MaxBundleFiles {
			return errors.New("the bundle has too many files")
		}
		if total += h.Size; total > pack.MaxBundleBytes {
			return errors.New("the bundle is too large")
		}
		target := filesDir + "/" + name
		if err := addDir(path.Dir(target)); err != nil {
			return err
		}
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: target, Mode: 0o444, Size: h.Size, Uid: 65534, Gid: 65534, ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.CopyN(tw, tr, h.Size); err != nil {
			return errors.New("the bundle is damaged")
		}
	}
}
