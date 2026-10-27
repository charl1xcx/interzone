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

// ExtraGroups splits ExtraYAML by the component each top-level key belongs
// to, judged by the longest matching variable prefix. Comments go with the
// key under them. Every enabled component gets a group, empty or not, and
// the homeserver comes first and Other last.
func (c *Config) ExtraGroups() []ExtraGroup {
	hs := c.Homeserver
	for _, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			hs = h.Name
		}
	}
	names := []string{hs}
	prefix := map[string]string{"matrix_" + c.Homeserver: hs}
	for _, comp := range catalog.Components {
		for _, v := range append(hostVars(comp), comp.HostVar()) {
			if p, ok := strings.CutSuffix(v, "_hostname"); ok && p != "" {
				prefix[p] = comp.Name
			}
		}
		if c.Enabled(comp) && !slices.Contains(names, comp.Name) {
			names = append(names, comp.Name)
		}
	}
	owner := func(key string) string {
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

	text := map[string][]string{}
	var block []string
	flush := func(name string) {
		if b := strings.TrimRight(strings.Join(block, "\n"), "\n "); strings.TrimSpace(b) != "" {
			text[name] = append(text[name], b)
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
			cur = owner(strings.TrimSpace(k))
		}
		block = append(block, line)
	}
	flush(cur)

	for _, n := range slices.Sorted(maps.Keys(text)) {
		if !slices.Contains(names, n) && n != "Other" {
			names = append(names, n) // a component that is off, still holding vars
		}
	}
	names = append(names, "Other")
	out := make([]ExtraGroup, len(names))
	for i, n := range names {
		out[i] = ExtraGroup{n, strings.Join(text[n], "\n")}
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
