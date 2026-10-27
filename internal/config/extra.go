package config

import (
	"maps"
	"slices"
	"strings"

	"interzone/internal/catalog"
)

// ExtraGroup is the hand-written extra vars that belong to one component
// (or to the homeserver, or to none: "Other").
type ExtraGroup struct {
	Name string
	Text string
}

// ExtraGroups splits ExtraYAML by the playbook role each top-level key
// belongs to: the role whose defaults define it, else the role or
// component with the longest matching name prefix, else "Other". A group
// is named after interzone's component when there is one. Comments go
// with the key under them. Only groups holding vars are listed, in the
// order they first appear, the homeserver first and Other last.
func (c *Config) ExtraGroups() []ExtraGroup {
	hs := c.Homeserver
	for _, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			hs = h.Name
		}
	}
	// What each role is called: the component it runs, if interzone has one.
	byVar := map[string]string{}
	for _, comp := range catalog.Components {
		for _, v := range append([]string{comp.EnableVar, comp.HostVar()}, slices.Collect(maps.Keys(comp.Extra))...) {
			byVar[v] = comp.Name
		}
	}
	owner := map[string]string{}  // variable → group
	prefix := map[string]string{} // name prefix → group
	roles, _ := Roles(c.PlaybookDir)
	for _, r := range roles {
		name := r.Name
		if r.Name == HomeserverRole[c.Homeserver] {
			name = hs
		} else {
			for _, v := range r.Vars {
				if n, ok := byVar[v.Name]; ok && r.Name != PlaybookRole {
					name = n
					break
				}
			}
		}
		for _, v := range r.Vars {
			if _, ok := owner[v.Name]; !ok {
				owner[v.Name] = name
			}
		}
		if r.Name != PlaybookRole {
			prefix[strings.ReplaceAll(r.Name, "-", "_")] = name
		}
	}
	// Without the playbook (or for a key no role defines), names do.
	for _, comp := range catalog.Components {
		for _, v := range append(hostVars(comp), comp.HostVar()) {
			if p, ok := strings.CutSuffix(v, "_hostname"); ok && p != "" {
				if _, taken := prefix[p]; !taken {
					prefix[p] = comp.Name
				}
			}
		}
	}
	prefix["matrix_"+c.Homeserver] = hs
	group := func(key string) string {
		if n, ok := owner[key]; ok {
			return n
		}
		best := ""
		for p := range prefix {
			if (key == p || strings.HasPrefix(key, p+"_")) && len(p) > len(best) {
				best = p
			}
		}
		if best == "" {
			return "Other"
		}
		return prefix[best]
	}

	names := []string{hs}
	text := map[string][]string{}
	var block []string
	flush := func(name string) {
		if b := strings.TrimRight(strings.Join(block, "\n"), "\n "); strings.TrimSpace(b) != "" {
			text[name] = append(text[name], b)
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		block = nil
	}
	cur := "Other"
	for _, line := range strings.Split(strings.TrimSpace(c.ExtraYAML), "\n") {
		if k, _, ok := strings.Cut(line, ":"); ok && k != "" && !strings.ContainsAny(k[:1], " \t#-") {
			// Comments and blank lines just above the key are its own.
			j := len(block)
			for j > 0 && onlyComments(block[j-1:j]) {
				j--
			}
			lead := slices.Clone(block[j:])
			block = block[:j]
			flush(cur)
			block = lead
			cur = group(strings.TrimSpace(k))
		}
		block = append(block, line)
	}
	flush(cur)

	var out []ExtraGroup
	for _, n := range append(slices.DeleteFunc(names, func(n string) bool { return n == "Other" }), "Other") {
		if t := text[n]; len(t) > 0 || n == "Other" {
			out = append(out, ExtraGroup{n, strings.Join(t, "\n")})
		}
	}
	return out
}

func onlyComments(lines []string) bool {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return false
		}
	}
	return true
}

// JoinExtra is ExtraYAML made back from its groups.
func JoinExtra(groups []ExtraGroup) string {
	var parts []string
	for _, g := range groups {
		if t := strings.TrimSpace(g.Text); t != "" {
			parts = append(parts, strings.TrimRight(g.Text, "\n "))
		}
	}
	return strings.Join(parts, "\n")
}
