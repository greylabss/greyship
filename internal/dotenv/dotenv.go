// Package dotenv reads and writes .env files (spec F-39a): KEY=VALUE lines, # comments, an optional "export ",
// double quotes with \n \t \\ \" escapes (which may span lines), single quotes taken literally.
package dotenv

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse reads a .env file. Later lines win for repeated keys, as in most loaders.
func Parse(data string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", i+1)
		}
		raw = strings.TrimLeft(raw, " \t")
		switch {
		case strings.HasPrefix(raw, `"`):
			value, consumed, err := doubleQuoted(raw[1:], lines[i+1:])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			out[key] = value
			i += consumed
		case strings.HasPrefix(raw, "'"):
			end := strings.Index(raw[1:], "'")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unclosed single quote", i+1)
			}
			out[key] = raw[1 : end+1]
		default:
			if j := strings.Index(raw, " #"); j >= 0 {
				raw = raw[:j]
			}
			out[key] = strings.TrimSpace(raw)
		}
	}
	return out, nil
}

// doubleQuoted reads up to the closing quote, continuing onto following lines; it returns how many extra
// lines it used.
func doubleQuoted(first string, rest []string) (string, int, error) {
	var b strings.Builder
	text, used := first, 0
	for {
		for j := 0; j < len(text); j++ {
			c := text[j]
			switch {
			case c == '\\' && j+1 < len(text):
				j++
				switch text[j] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				default:
					b.WriteByte(text[j])
				}
			case c == '"':
				return b.String(), used, nil
			default:
				b.WriteByte(c)
			}
		}
		if used >= len(rest) {
			return "", 0, fmt.Errorf("unclosed double quote")
		}
		b.WriteByte('\n')
		text = rest[used]
		used++
	}
}

// Format writes values as a .env file, keys sorted, every value double-quoted and escaped so it reads back exactly.
func Format(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(values[k])
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, v)
	}
	return b.String()
}
