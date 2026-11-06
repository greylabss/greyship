package pack

import (
	"bufio"
	"io"
	"path"
	"strings"
)

// rule is one line of a .gitignore or .greyshipignore, relative to the folder that holds the file.
type rule struct {
	base    string   // slash path of that folder, "" for the root
	parts   []string // pattern split on "/"
	negate  bool
	dirOnly bool
}

// parseIgnore reads gitignore syntax: comments, blank lines, "!" to re-include, a trailing "/" for folders only,
// a "/" anywhere else to anchor the pattern to base, and "*", "?", "[…]" and "**" wildcards.
func parseIgnore(r io.Reader, base string) []rule {
	var rules []rule
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var ru rule
		ru.base = base
		if strings.HasPrefix(line, "!") {
			ru.negate, line = true, line[1:]
		} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			ru.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		// Without a slash the pattern matches a name at any depth below base.
		if !strings.Contains(line, "/") {
			line = "**/" + line
		}
		ru.parts = strings.Split(strings.TrimPrefix(line, "/"), "/")
		rules = append(rules, ru)
	}
	return rules
}

// ignored applies rules in order; the last one that matches decides, as in git.
func ignored(rules []rule, rel string, isDir bool) bool {
	out := false
	for _, ru := range rules {
		if ru.dirOnly && !isDir {
			continue
		}
		sub, ok := within(ru.base, rel)
		if !ok {
			continue
		}
		if matchParts(ru.parts, strings.Split(sub, "/")) {
			out = !ru.negate
		}
	}
	return out
}

func within(base, rel string) (string, bool) {
	if base == "" {
		return rel, true
	}
	if strings.HasPrefix(rel, base+"/") {
		return rel[len(base)+1:], true
	}
	return "", false
}

func matchParts(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		// A trailing "/**" matches everything inside, but not the folder itself.
		if len(pat) == 1 {
			return len(name) > 0
		}
		for i := 0; i <= len(name); i++ {
			if matchParts(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	return err == nil && ok && matchParts(pat[1:], name[1:])
}
