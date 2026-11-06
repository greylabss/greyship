package deploy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"greyship.tech/internal/objstore"
	"greyship.tech/internal/pack"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// Local builds (decisions.md 034): the CLI compiles on the developer's machine and sends the binaries.
const (
	maxBinaries   = 20
	maxBinaryRaw  = 200 << 20
	maxBinaryGzip = 100 << 20
)

var (
	serviceName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	servicePath  = regexp.MustCompile(`^\.(/[A-Za-z0-9_.\-]+)*$`)
	ErrBadBinary = errors.New("invalid binary list")
)

// Binary is one compiled service in a local build, described before it is uploaded.
type Binary struct {
	Service   string
	Path      string
	Type      string // web | worker | release
	SHA256    string
	SizeBytes int64 // uncompressed
	GzBytes   int64 // as uploaded (gzip)
}

// BinaryUpload is where to PUT one binary. Binaries the project already has need no upload.
type BinaryUpload struct {
	Service string
	URL     string
}

// Files is a local build's files bundle (greyship.toml's files), described before it is uploaded.
type Files struct {
	SHA256    string
	SizeBytes int64 // as uploaded (tar.gz)
}

type LocalUpload struct {
	Deployment Deployment
	Uploads    []BinaryUpload
	FilesURL   string // empty when there is no bundle or the project already has it
}

func checkBinaries(bins []Binary) error {
	if len(bins) == 0 || len(bins) > maxBinaries {
		return fmt.Errorf("%w: send 1 to %d binaries", ErrBadBinary, maxBinaries)
	}
	seen, releases := map[string]bool{}, 0
	for _, b := range bins {
		switch {
		case !serviceName.MatchString(b.Service) || seen[b.Service]:
			return fmt.Errorf("%w: service name %q", ErrBadBinary, b.Service)
		case !servicePath.MatchString(b.Path) || strings.Contains(b.Path, "/.."):
			return fmt.Errorf("%w: path %q", ErrBadBinary, b.Path)
		case b.Type != "web" && b.Type != "worker" && b.Type != "cron" && b.Type != "release":
			return fmt.Errorf("%w: type %q", ErrBadBinary, b.Type)
		case !sha256Pattern.MatchString(b.SHA256):
			return fmt.Errorf("%w: sha256 of %s", ErrBadBinary, b.Service)
		case b.SizeBytes <= 0 || b.SizeBytes > maxBinaryRaw || b.GzBytes <= 0 || b.GzBytes > maxBinaryGzip:
			return fmt.Errorf("%w: %s is too large (at most 200 MB, 100 MB compressed)", ErrBadBinary, b.Service)
		}
		seen[b.Service] = true
		if b.Type == "release" {
			releases++
		}
	}
	if releases > 1 {
		return fmt.Errorf("%w: at most one release command", ErrBadBinary)
	}
	return nil
}

// binaryKey is content-addressed within a project: an unchanged service is never uploaded twice. Keys are never
// shared between projects.
func binaryKey(projectID, sha string) string { return "binaries/" + projectID + "/" + sha + ".gz" }

// FilesKey is where a project's files bundle is stored, content-addressed like binaries.
func FilesKey(projectID, sha string) string { return "files/" + projectID + "/" + sha + ".tar.gz" }

// CreateLocal records a local build and returns upload URLs for the binaries the project doesn't have yet.
func (s *Service) CreateLocal(ctx context.Context, c project.Caller, projectID, commitSHA string, bins []Binary, files *Files) (LocalUpload, error) {
	if err := checkBinaries(bins); err != nil {
		return LocalUpload{}, err
	}
	if files != nil && (!sha256Pattern.MatchString(files.SHA256) || files.SizeBytes <= 0 || files.SizeBytes > pack.MaxBundleBytes) {
		return LocalUpload{}, fmt.Errorf("%w: files bundle (at most %d MB)", ErrBadBinary, pack.MaxBundleBytes>>20)
	}
	if commitSHA != "" && !commitPattern.MatchString(commitSHA) {
		return LocalUpload{}, ErrBadSource
	}
	p, err := s.projects.Access(ctx, c, projectID, true)
	if err != nil {
		return LocalUpload{}, err
	}
	id, err := newID()
	if err != nil {
		return LocalUpload{}, err
	}
	var commit *string
	if commitSHA != "" {
		commit = &commitSHA
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LocalUpload{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	d, err := q.CreateLocalDeployment(ctx, store.CreateLocalDeploymentParams{ID: id, ProjectID: p.ID, CommitSha: commit, CreatedBy: &c.UserID})
	if err != nil {
		return LocalUpload{}, fmt.Errorf("create deployment for %s: %w", p.ID, err)
	}
	var uploads []BinaryUpload
	for _, b := range bins {
		key := binaryKey(p.ID, b.SHA256)
		if err := q.AddUploadedBinary(ctx, store.AddUploadedBinaryParams{
			DeploymentID: id, ServiceName: b.Service, ServicePath: b.Path, ServiceType: b.Type,
			Sha256: b.SHA256, SizeBytes: b.SizeBytes, GzBytes: b.GzBytes, ObjectKey: key,
		}); err != nil {
			return LocalUpload{}, fmt.Errorf("record binary %s: %w", b.Service, err)
		}
		if size, err := s.storage.Stat(ctx, key); err == nil && size == b.GzBytes {
			continue
		} else if err != nil && !errors.Is(err, objstore.ErrNotFound) {
			return LocalUpload{}, fmt.Errorf("check stored binary %s: %w", b.Service, err)
		}
		url, err := s.storage.PresignPut(key, b.GzBytes, uploadTTL)
		if err != nil {
			return LocalUpload{}, fmt.Errorf("presign upload: %w", err)
		}
		uploads = append(uploads, BinaryUpload{Service: b.Service, URL: url})
	}
	var filesURL string
	if files != nil {
		key := FilesKey(p.ID, files.SHA256)
		if err := q.SetDeploymentFiles(ctx, store.SetDeploymentFilesParams{ID: id, FilesKey: &key, FilesSha256: &files.SHA256, FilesSize: &files.SizeBytes}); err != nil {
			return LocalUpload{}, fmt.Errorf("record files of %s: %w", id, err)
		}
		d.FilesKey, d.FilesSha256, d.FilesSize = &key, &files.SHA256, &files.SizeBytes
		if filesURL, err = s.uploadURL(ctx, key, files.SizeBytes); err != nil {
			return LocalUpload{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return LocalUpload{}, fmt.Errorf("commit deployment for %s: %w", p.ID, err)
	}
	return LocalUpload{Deployment: Deployment{Deployment: d, Project: p}, Uploads: uploads, FilesURL: filesURL}, nil
}

// uploadURL is a presigned PUT for key, or "" when it is already stored with that size.
func (s *Service) uploadURL(ctx context.Context, key string, size int64) (string, error) {
	if got, err := s.storage.Stat(ctx, key); err == nil && got == size {
		return "", nil
	} else if err != nil && !errors.Is(err, objstore.ErrNotFound) {
		return "", fmt.Errorf("check stored %s: %w", key, err)
	}
	url, err := s.storage.PresignPut(key, size, uploadTTL)
	if err != nil {
		return "", fmt.Errorf("presign upload: %w", err)
	}
	return url, nil
}

// checkUploads confirms every binary of a local build, and its files bundle, is stored with the size declared.
func (s *Service) checkUploads(ctx context.Context, d store.Deployment) error {
	if d.FilesKey != nil && d.FilesSize != nil {
		size, err := s.storage.Stat(ctx, *d.FilesKey)
		if err != nil && !errors.Is(err, objstore.ErrNotFound) {
			return fmt.Errorf("check files of %s: %w", d.ID, err)
		}
		if err != nil || size != *d.FilesSize {
			return ErrUploadMissing
		}
	}
	deploymentID := d.ID
	bins, err := store.New(s.pool).ListUploadedBinaries(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("list binaries of %s: %w", deploymentID, err)
	}
	for _, b := range bins {
		size, err := s.storage.Stat(ctx, b.ObjectKey)
		if err != nil && !errors.Is(err, objstore.ErrNotFound) {
			return fmt.Errorf("check binary %s: %w", b.ServiceName, err)
		}
		if err != nil || size != b.GzBytes {
			return ErrUploadMissing
		}
	}
	return nil
}
