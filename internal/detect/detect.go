// Package detect finds the services in a Go repo with no configuration (spec F-11 to F-15, decisions 015–017).
// It is pure: files in, service list out. No network, no build.
package detect

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	Web    = "web"
	Worker = "worker"
	Cron   = "cron"
)

type Service struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // "./cmd/api", or "." for the repo root
	Type     string `json:"type"`
	Schedule string `json:"schedule,omitempty"`
	Reason   string `json:"reason"`
}

// Skipped is a main package that is not deployed, with the reason shown to the user.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Result struct {
	GoVersion string    `json:"go_version"` // toolchain, else the go directive; empty means latest stable (F-12)
	Services  []Service `json:"services"`
	Release   *Service  `json:"release,omitempty"` // runs once per deploy before traffic switches (F-29)
	Skipped   []Skipped `json:"skipped,omitempty"`
	Files     []string  `json:"files,omitempty"` // greyship.toml's files: shipped next to every binary
	// Warnings are hints that never fail a deploy, such as a repo file read at runtime that isn't shipped.
	Warnings []string `json:"warnings,omitempty"`
}

// ConfigError is a detection failure the user fixes, with the fix spelled out (F-15).
type ConfigError struct {
	Msg string
	Fix string
}

func (e *ConfigError) Error() string {
	if e.Fix == "" {
		return e.Msg
	}
	return e.Msg + "\nfix: " + e.Fix
}

const releaseDir = "cmd/migrate"

// Detect lists the services in the module at the root of fsys. project is the project slug, used to name a root main.
func Detect(fsys fs.FS, project string) (Result, error) {
	mod, err := readModule(fsys)
	if err != nil {
		return Result{}, err
	}
	cfg, err := readConfig(fsys)
	if err != nil {
		return Result{}, err
	}
	mains, err := findMains(fsys, mod.Path)
	if err != nil {
		return Result{}, err
	}

	res := Result{GoVersion: mod.GoVersion, Files: cfg.Files}
	a := newAnalyzer(fsys, mod.Path)
	var dirs []string
	for _, m := range mains {
		if m.skip != "" {
			res.Skipped = append(res.Skipped, Skipped{Path: display(m.dir), Reason: m.skip})
			continue
		}
		dirs = append(dirs, m.dir)
	}

	releaseAt := releaseDir
	if cfg.Release != nil && cfg.Release.Command != "" {
		releaseAt = clean(cfg.Release.Command)
		if !hasMain(mains, releaseAt) {
			return Result{}, &ConfigError{Msg: fmt.Sprintf("[release] command %s has no package main", cfg.Release.Command), Fix: "point command at a folder with a main package, e.g. command = \"./cmd/migrate\""}
		}
	}
	var services []Service
	for _, dir := range dirs {
		if dir == releaseAt {
			continue
		}
		if dir == releaseDir {
			res.Skipped = append(res.Skipped, Skipped{Path: display(dir), Reason: "[release] in greyship.toml points elsewhere"})
			continue
		}
		services = append(services, Service{Path: display(dir)})
	}
	if hasMain(mains, releaseAt) {
		reason := "cmd/migrate is the release command"
		if releaseAt != releaseDir {
			reason = "[release] in greyship.toml"
		}
		res.Release = &Service{Name: "release", Path: display(releaseAt), Type: Worker, Reason: reason}
		res.Skipped = removeSkipped(res.Skipped, display(releaseAt))
	}

	nameServices(services, project)
	services, res.Skipped, err = applyConfig(cfg, services, mains, res.Skipped, releaseAt)
	if err != nil {
		return Result{}, err
	}
	for i := range services {
		if services[i].Type != "" {
			continue
		}
		typ, reason, err := a.kind(clean(services[i].Path))
		if err != nil {
			return Result{}, err
		}
		services[i].Type, services[i].Reason = typ, reason
	}
	if err := checkUnique(services); err != nil {
		return Result{}, err
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	sort.Slice(res.Skipped, func(i, j int) bool { return res.Skipped[i].Path < res.Skipped[j].Path })
	res.Services = services
	// A hint only: if the scan itself fails, the deploy goes on without it.
	res.Warnings, _ = runtimeReads(fsys, cfg.Files)
	return res, nil
}

func hasMain(mains []mainDir, dir string) bool {
	for _, m := range mains {
		if m.dir == dir {
			return true
		}
	}
	return false
}

func removeSkipped(s []Skipped, p string) []Skipped {
	out := s[:0]
	for _, x := range s {
		if x.Path != p {
			out = append(out, x)
		}
	}
	return out
}

// display turns a slash path relative to the repo root into the form users see: "./cmd/api", or "." for the root.
func display(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// clean turns "./cmd/api/", "cmd/api" or "." into the internal form "cmd/api" / ".".
func clean(p string) string {
	p = path.Clean(strings.TrimPrefix(strings.TrimSpace(p), "./"))
	if p == "" || p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

var errNoModule = &ConfigError{Msg: "no go.mod at the repo root", Fix: "run go mod init <module path> and commit go.mod"}

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
