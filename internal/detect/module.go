package detect

import (
	"fmt"
	"io/fs"
	"strings"
)

type module struct {
	Path      string
	GoVersion string
}

// readModule reads the module path and Go version from go.mod. The few directives needed don't justify a parser dependency.
func readModule(fsys fs.FS) (module, error) {
	b, err := fs.ReadFile(fsys, "go.mod")
	if isNotExist(err) {
		return module{}, errNoModule
	}
	if err != nil {
		return module{}, fmt.Errorf("read go.mod: %w", err)
	}
	var m module
	var goDirective, toolchain string
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value := strings.Trim(fields[1], "\"`")
		switch fields[0] {
		case "module":
			m.Path = value
		case "go":
			goDirective = value
		case "toolchain":
			if value != "default" {
				toolchain = strings.TrimPrefix(value, "go")
			}
		}
	}
	if m.Path == "" {
		return module{}, &ConfigError{Msg: "go.mod has no module line", Fix: "add a line like: module github.com/you/app"}
	}
	m.GoVersion = goDirective
	if toolchain != "" {
		m.GoVersion = toolchain
	}
	return m, nil
}
