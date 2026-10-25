package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// RoleVar is one variable from a role's defaults/main.yml.
type RoleVar struct {
	Name    string
	Default string // raw YAML, as written in the file
	Help    string // the comment block above it, without the #s
	Derived bool   // the default is computed from other variables
	// Playbook is what the playbook's group_vars/matrix_servers sets
	// instead of the role's default, when PlaybookSet. It wins over the
	// role default and is what applies unless the variable is changed.
	Playbook    string
	PlaybookSet bool
}

// Effective is the value that applies when the variable isn't changed.
func (v RoleVar) Effective() string {
	if v.PlaybookSet {
		return v.Playbook
	}
	return v.Default
}

// PlaybookRole names the pseudo-role for variables the playbook's
// group_vars defines without a role default.
const PlaybookRole = "playbook-wide"

// HomeserverRole is the playbook role holding each homeserver's defaults.
var HomeserverRole = map[string]string{
	"synapse":      "matrix-synapse",
	"continuwuity": "matrix-continuwuity",
	"tuwunel":      "matrix-tuwunel",
	"conduit":      "matrix-conduit",
	"dendrite":     "matrix-dendrite",
}

// Role is a playbook role with variables to tune: one of the playbook's
// own (roles/custom) or one installed from requirements.yml (roles/galaxy).
type Role struct {
	Name   string // directory name, e.g. matrix-synapse or ntfy
	Galaxy bool
	Vars   []RoleVar
}

// Roles reads the defaults of every role in the playbook that has them,
// custom roles first, each group sorted by name. Galaxy roles exist only
// once they've been installed (the install's Roles step).
func Roles(playbookDir string) ([]Role, error) {
	var out []Role
	for _, kind := range []string{"custom", "galaxy"} {
		files, _ := filepath.Glob(filepath.Join(playbookDir, "roles", kind, "*", "defaults", "main.yml"))
		more, _ := filepath.Glob(filepath.Join(playbookDir, "roles", kind, "*", "defaults", "main", "*.yml"))
		byRole := map[string][]string{}
		var names []string
		for _, f := range append(files, more...) {
			rel, _ := filepath.Rel(filepath.Join(playbookDir, "roles", kind), f)
			name := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if byRole[name] == nil {
				names = append(names, name)
			}
			byRole[name] = append(byRole[name], f)
		}
		sort.Strings(names)
		for _, name := range names {
			r := Role{Name: name, Galaxy: kind == "galaxy"}
			for _, f := range byRole[name] {
				b, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				r.Vars = append(r.Vars, ParseDefaults(string(b))...)
			}
			if len(r.Vars) > 0 {
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no roles with defaults in %s", filepath.Join(playbookDir, "roles"))
	}
	// The playbook wires the roles together in group_vars, overriding
	// many of their defaults.
	b, _ := os.ReadFile(filepath.Join(playbookDir, "group_vars", "matrix_servers"))
	group := map[string]RoleVar{}
	var order []string
	for _, v := range ParseDefaults(string(b)) {
		if _, dup := group[v.Name]; !dup {
			order = append(order, v.Name)
		}
		group[v.Name] = v
	}
	seen := map[string]bool{}
	for i := range out {
		for j := range out[i].Vars {
			v := &out[i].Vars[j]
			if g, ok := group[v.Name]; ok {
				v.Playbook, v.PlaybookSet = g.Default, true
				v.Derived = g.Derived
				if v.Help == "" {
					v.Help = g.Help
				}
			}
			seen[v.Name] = true
		}
	}
	wide := Role{Name: PlaybookRole}
	for _, name := range order {
		if !seen[name] {
			wide.Vars = append(wide.Vars, group[name])
		}
	}
	if len(wide.Vars) > 0 {
		out = append([]Role{wide}, out...)
	}
	return out, nil
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
