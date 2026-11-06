package detect

import (
	"fmt"
	"path"
	"strings"

	"greyship.tech/internal/slug"
)

const maxNameLen = 40

// Folders that say nothing about which service lives below them, so they're skipped when prefixing a parent.
var genericParents = map[string]bool{"cmd": true, "app": true, "apps": true, "services": true, "service": true, "svc": true, "internal": true, "src": true, "server": true, "servers": true}

// nameServices names each service by its folder (F-11): the root and cmd/ mains take the project name, cmd/<x> is x,
// anything else its own folder. Clashes get the nearest meaningful parent prefixed until names are unique.
// Clashes that remain are left for greyship.toml to resolve; Detect checks uniqueness at the end.
func nameServices(services []Service, project string) {
	depth := make([]int, len(services))
	for i := range services {
		services[i].Name = baseName(clean(services[i].Path), project)
	}
	for {
		groups := map[string][]int{}
		for i, s := range services {
			groups[s.Name] = append(groups[s.Name], i)
		}
		changed := false
		for _, idx := range groups {
			if len(idx) < 2 {
				continue
			}
			for _, i := range idx {
				prefix, next := parentPrefix(clean(services[i].Path), depth[i])
				if prefix == "" {
					continue
				}
				depth[i] = next
				services[i].Name = slug.Make(prefix+"-"+services[i].Name, maxNameLen)
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func baseName(dir, project string) string {
	if dir == "." || dir == "cmd" {
		return project
	}
	return slug.Make(path.Base(dir), maxNameLen)
}

// parentPrefix returns the next meaningful ancestor folder of dir above the one used last time (from).
func parentPrefix(dir string, from int) (string, int) {
	parts := strings.Split(dir, "/")
	for i := len(parts) - 2 - from; i >= 0; i-- {
		if !genericParents[parts[i]] {
			return parts[i], len(parts) - 1 - i
		}
	}
	return "", from
}

func checkUnique(services []Service) error {
	seen := map[string]string{}
	for _, s := range services {
		if other, ok := seen[s.Name]; ok {
			return &ConfigError{
				Msg: fmt.Sprintf("two services would both be named %q: %s and %s", s.Name, other, s.Path),
				Fix: fmt.Sprintf("name one of them in greyship.toml:\n  [services.%s-2]\n  path = %q", s.Name, s.Path),
			}
		}
		seen[s.Name] = s.Path
	}
	return nil
}
