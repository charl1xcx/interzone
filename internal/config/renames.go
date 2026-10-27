package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// renamed matches the playbook's deprecation lists in its roles' tasks:
// - {'old': 'matrix_x_additional_arguments', 'new': 'matrix_x_extra_arguments'}
// Entries whose 'new' is a note ("<removed> …") aren't renames and don't match.
var renamed = regexp.MustCompile(`\{\s*['"]old['"]:\s*['"]([a-z0-9_]+)['"],\s*['"]new['"]:\s*['"]([a-z0-9_]+)['"]\s*\}`)

// Renames reads every variable the playbook has renamed, old name to the
// current one, following chains (a → b → c gives a → c).
func Renames(playbookDir string) map[string]string {
	files, _ := filepath.Glob(filepath.Join(playbookDir, "roles", "*", "*", "tasks", "*.yml"))
	r := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range renamed.FindAllStringSubmatch(string(b), -1) {
			r[m[1]] = m[2]
		}
	}
	for old, n := range r {
		for i := 0; i < 10 && r[n] != ""; i++ {
			n = r[n]
		}
		r[old] = n
	}
	return r
}

// RenameOld moves every setting still under a name the playbook has
// renamed to its current name, so the playbook's deprecation check doesn't
// stop the deploy. It returns "old → new" for each. When the new name is
// already set too, the old setting is dropped (commented out in extra vars).
func (c *Config) RenameOld(playbookDir string) []string {
	r := Renames(playbookDir)
	if len(r) == 0 {
		return nil
	}
	var done []string
	for _, m := range []map[string]string{c.Values, c.Overrides} {
		for old, v := range m {
			n, ok := r[old]
			if !ok {
				continue
			}
			if _, set := m[n]; !set {
				m[n] = v
			}
			delete(m, old)
			done = append(done, old+" → "+n)
		}
	}

	lines := strings.Split(c.ExtraYAML, "\n")
	keys := map[string]bool{}
	for _, l := range lines {
		if m := topKey.FindStringSubmatch(l); m != nil {
			keys[m[1]] = true
		}
	}
	drop := false
	for i, l := range lines {
		if m := topKey.FindStringSubmatch(l); m != nil {
			k := m[1]
			drop = false
			if n, old := r[k]; old {
				done = append(done, k+" → "+n)
				if keys[n] {
					drop = true
				} else {
					lines[i] = n + l[len(k):]
					keys[n] = true
				}
			}
		}
		if drop && l != "" {
			lines[i] = "# " + l
		}
	}
	c.ExtraYAML = strings.Join(lines, "\n")
	return done
}
