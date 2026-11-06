package detect

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/robfig/cron/v3"

	"greyship.tech/internal/pack"
	"greyship.tech/internal/slug"
)

const configFile = "greyship.toml"

// config is greyship.toml (F-13). Only path, type, schedule, exclude and [release] change detection; the other
// fields are accepted here so files written for later features don't fail, and take effect in later steps.
type config struct {
	// Files are repo files and folders shipped next to every service's binary (internal/pack, files.go).
	Files    []string                 `toml:"files"`
	Services map[string]serviceConfig `toml:"services"`
	Release  *releaseConfig           `toml:"release"`
	Routes   []routeConfig            `toml:"routes"`
}

type serviceConfig struct {
	Path         string  `toml:"path"`
	Type         string  `toml:"type"`
	Schedule     string  `toml:"schedule"`
	Exclude      bool    `toml:"exclude"`
	Memory       string  `toml:"memory"`
	CPU          float64 `toml:"cpu"`
	MinInstances int     `toml:"min_instances"`
	MaxInstances int     `toml:"max_instances"`
	Concurrency  int     `toml:"concurrency"`
	Grace        string  `toml:"grace"`
	HealthPath   string  `toml:"health_path"`
	Region       string  `toml:"region"`
	Visibility   string  `toml:"visibility"`
	Protocol     string  `toml:"protocol"`
}

type releaseConfig struct {
	Command string `toml:"command"`
	Timeout string `toml:"timeout"`
}

type routeConfig struct {
	Domain  string `toml:"domain"`
	Path    string `toml:"path"`
	Service string `toml:"service"`
}

func readConfig(fsys fs.FS) (config, error) {
	var cfg config
	b, err := fs.ReadFile(fsys, configFile)
	if isNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", configFile, err)
	}
	md, err := toml.Decode(string(b), &cfg)
	if err != nil {
		return cfg, &ConfigError{Msg: fmt.Sprintf("%s is not valid: %v", configFile, err)}
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0]
		if len(key) > 1 && key[len(key)-1] == "files" {
			return cfg, &ConfigError{Msg: fmt.Sprintf("%s has files inside [%s]", configFile, strings.Join(key[:len(key)-1], ".")), Fix: "move the files = [...] line to the top of greyship.toml, before any [section]"}
		}
		return cfg, &ConfigError{Msg: fmt.Sprintf("%s has an unknown setting: %s", configFile, key), Fix: "check the spelling; see the greyship.toml reference"}
	}
	if len(cfg.Files) > 100 {
		return cfg, &ConfigError{Msg: fmt.Sprintf("%s lists %d entries in files", configFile, len(cfg.Files)), Fix: "list folders instead of single files (at most 100 entries)"}
	}
	for _, e := range cfg.Files {
		if err := pack.CheckEntry(e); err != nil {
			return cfg, &ConfigError{Msg: fmt.Sprintf("%s: files entry %s", configFile, strings.TrimPrefix(err.Error(), pack.ErrUnsafeEntry.Error()+": ")), Fix: "list paths inside the repo, e.g. files = [\"templates/\", \"scripts/install.sh\"]"}
		}
	}
	return cfg, nil
}

// applyConfig layers greyship.toml over the detected services (F-14): a declared name matching a detected one
// overrides it; path renames a detected main or includes a skipped one; exclude drops it.
func applyConfig(cfg config, services []Service, mains []mainDir, skipped []Skipped, releaseAt string) ([]Service, []Skipped, error) {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sc := cfg.Services[name]
		if slug.Make(name, maxNameLen) != name || name[0] < 'a' || name[0] > 'z' {
			return nil, nil, &ConfigError{Msg: fmt.Sprintf("[services.%s]: service names use lowercase letters, digits and dashes and start with a letter", name)}
		}
		i := indexByName(services, name)
		if sc.Path != "" {
			dir := clean(sc.Path)
			if dir == releaseAt {
				return nil, nil, &ConfigError{Msg: fmt.Sprintf("[services.%s] path %s is the release command", name, sc.Path), Fix: "a folder is either a service or the [release] command, not both"}
			}
			if !hasMain(mains, dir) {
				return nil, nil, &ConfigError{Msg: fmt.Sprintf("[services.%s] path %s has no package main", name, sc.Path), Fix: "point path at a folder with a main package, e.g. path = \"./cmd/" + name + "\""}
			}
			if i >= 0 && clean(services[i].Path) != dir {
				return nil, nil, &ConfigError{Msg: fmt.Sprintf("[services.%s] path %s, but %s is already the detected %q", name, sc.Path, services[i].Path, name), Fix: "pick a different name for one of them"}
			}
			if j := indexByPath(services, display(dir)); j >= 0 {
				services[j].Name = name
				i = j
			} else {
				services = append(services, Service{Name: name, Path: display(dir)})
				skipped = removeSkipped(skipped, display(dir))
				i = len(services) - 1
			}
		}
		if i < 0 {
			return nil, nil, &ConfigError{Msg: fmt.Sprintf("[services.%s] doesn't match a detected service", name), Fix: fmt.Sprintf("add path = \"./path/to/%s\" to say where its main package is", name)}
		}
		if sc.Exclude {
			skipped = append(skipped, Skipped{Path: services[i].Path, Reason: "exclude = true in greyship.toml"})
			services = append(services[:i], services[i+1:]...)
			continue
		}
		if err := applyType(&services[i], name, sc); err != nil {
			return nil, nil, err
		}
	}
	return services, skipped, nil
}

func applyType(s *Service, name string, sc serviceConfig) error {
	switch sc.Type {
	case "":
		if sc.Schedule != "" {
			return &ConfigError{Msg: fmt.Sprintf("[services.%s] has a schedule but no type", name), Fix: "add type = \"cron\""}
		}
		return nil
	case Web, Worker:
		if sc.Schedule != "" {
			return &ConfigError{Msg: fmt.Sprintf("[services.%s] is a %s, so schedule doesn't apply", name, sc.Type), Fix: "remove schedule, or set type = \"cron\""}
		}
	case Cron:
		if sc.Schedule == "" {
			return &ConfigError{Msg: fmt.Sprintf("[services.%s] is a cron with no schedule", name), Fix: "add a schedule, e.g. schedule = \"0 3 * * *\""}
		}
		if _, err := cron.ParseStandard(sc.Schedule); err != nil {
			return &ConfigError{Msg: fmt.Sprintf("[services.%s] schedule %q is not a valid cron expression: %v", name, sc.Schedule, err), Fix: "use five fields: minute hour day-of-month month day-of-week, e.g. \"0 3 * * *\""}
		}
		s.Schedule = sc.Schedule
	default:
		return &ConfigError{Msg: fmt.Sprintf("[services.%s] type %q is unknown", name, sc.Type), Fix: "use web, worker or cron"}
	}
	s.Type = sc.Type
	s.Reason = fmt.Sprintf("type = %q in %s", sc.Type, configFile)
	return nil
}

func indexByName(services []Service, name string) int {
	for i, s := range services {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func indexByPath(services []Service, p string) int {
	for i, s := range services {
		if s.Path == p {
			return i
		}
	}
	return -1
}
