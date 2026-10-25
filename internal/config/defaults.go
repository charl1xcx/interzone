package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RoleVar is one variable from a role's defaults/main.yml.
type RoleVar struct {
	Name    string
	Default string // raw YAML, as written in the file
	Help    string // the comment block above it, without the #s
	Derived bool   // the default is computed from other variables
}

// HomeserverRole is the playbook role holding each homeserver's defaults.
var HomeserverRole = map[string]string{
	"synapse":      "matrix-synapse",
	"continuwuity": "matrix-continuwuity",
	"tuwunel":      "matrix-tuwunel",
	"conduit":      "matrix-conduit",
	"dendrite":     "matrix-dendrite",
}

// HomeserverDefaultsFile is the defaults file of the homeserver's role.
func HomeserverDefaultsFile(playbookDir, homeserver string) string {
	return filepath.Join(playbookDir, "roles", "custom", HomeserverRole[homeserver], "defaults", "main.yml")
}

// HomeserverDefaults lists every variable the homeserver's role lets you change.
func HomeserverDefaults(playbookDir, homeserver string) ([]RoleVar, error) {
	if HomeserverRole[homeserver] == "" {
		return nil, fmt.Errorf("unknown homeserver %q", homeserver)
	}
	b, err := os.ReadFile(HomeserverDefaultsFile(playbookDir, homeserver))
	if err != nil {
		return nil, err
	}
	return ParseDefaults(string(b)), nil
}

var topKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):(?:\s+(.*))?$`)

// ParseDefaults reads the top-level variables of a defaults file. A value
// runs until the next line that starts in column 0; the comments right
// above a variable are its help.
func ParseDefaults(text string) []RoleVar {
	var out []RoleVar
	var comments []string
	cur := -1
	var body []string
	flush := func() {
		if cur < 0 {
			return
		}
		v := strings.TrimRight(strings.Join(body, "\n"), "\n ")
		out[cur].Default = v
		out[cur].Derived = strings.Contains(v, "{{")
		cur, body = -1, nil
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			flush()
			c := strings.TrimPrefix(strings.TrimPrefix(line, "#"), " ")
			if strings.HasPrefix(c, "SPDX-") {
				continue
			}
			comments = append(comments, c)
		case line == "---":
			flush()
			comments = nil
		case strings.TrimSpace(line) == "":
			if cur >= 0 {
				body = append(body, "")
			} else {
				comments = nil // a blank line ends the comment block
			}
		case line[0] == ' ' || line[0] == '\t' || line[0] == '-' || line[0] == ']' || line[0] == '}':
			if cur >= 0 {
				body = append(body, line)
			}
		default:
			flush()
			m := topKey.FindStringSubmatch(line)
			if m == nil {
				comments = nil
				continue
			}
			out = append(out, RoleVar{Name: m[1], Help: strings.TrimSpace(strings.Join(comments, "\n"))})
			cur = len(out) - 1
			body = []string{stripComment(m[2])}
			comments = nil
		}
	}
	flush()
	for i := range out {
		v := out[i].Default
		// A value that starts on the next line (a list or map) loses the
		// empty first line.
		out[i].Default = strings.TrimPrefix(v, "\n")
		if strings.HasPrefix(v, "\n") {
			out[i].Default = dedent(out[i].Default)
		}
	}
	return out
}

// stripComment drops a trailing YAML comment (" # ...") from a one-line
// value, leaving any "#" inside quotes alone.
func stripComment(v string) string {
	var quote byte
	for i := 0; i < len(v); i++ {
		switch ch := v[i]; {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	min := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if min < 0 || n < min {
			min = n
		}
	}
	for i, l := range lines {
		if len(l) >= min && min > 0 {
			lines[i] = l[min:]
		}
	}
	return strings.Join(lines, "\n")
}
