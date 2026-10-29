package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"interzone/internal/catalog"
)

// ── Hostnames ──────────────────────────────────────────────────────
//
// Which hostnames the playbook will serve, worked out the way it does:
// each enabled component's <name>_hostname variable, from vars.yml or else
// the playbook's defaults, with {{ var }} references filled in the same
// way. A component whose hostname can't be worked out (no playbook yet, or
// a Jinja expression) gets its usual subdomain.

// DNSRecords lists the hostnames that must point at the server.
func (c *Config) DNSRecords() []string {
	recs, _ := c.hostTable()
	return recs
}

// ServicesOn names what is served on host, such as "Element Web, Cinny",
// or "" for a hostname this setup doesn't use.
func (c *Config) ServicesOn(host string) string { return c.HostServices()[host] }

// HostServices is ServicesOn for every hostname.
func (c *Config) HostServices() map[string]string {
	_, on := c.hostTable()
	out := map[string]string{}
	for h, what := range on {
		out[h] = strings.Join(what, ", ")
	}
	return out
}

// hostTable is DNSRecords with what each hostname serves.
func (c *Config) hostTable() ([]string, map[string][]string) {
	r := c.hostResolver()
	var recs []string
	on := map[string][]string{}
	serve := func(h, what string) {
		if h == "" {
			return
		}
		if !slices.Contains(recs, h) {
			recs = append(recs, h)
		}
		if !slices.Contains(on[h], what) {
			on[h] = append(on[h], what)
		}
	}
	hs := c.Homeserver
	for _, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			hs = h.Name
		}
	}
	serve(r.host("matrix_server_fqn_matrix", "matrix."+c.Domain), hs)
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) || comp.ID == "exim" { // exim's hostname is only the name it sends mail as
			continue
		}
		guess := ""
		if comp.Subdomain != "" {
			guess = comp.Subdomain + "." + c.Domain
		}
		found := false
		for _, v := range hostVars(comp) {
			if _, known := r.vars[v]; !known {
				if _, known = r.defaults[v]; !known {
					continue
				}
			}
			found = true
			serve(r.host(v, guess), comp.Name)
		}
		if !found {
			serve(guess, comp.Name)
		}
	}
	if c.Components["base-domain"] {
		serve(c.Domain, "base domain")
	}
	return recs, on
}

// hostVars are the variables that may hold a component's hostname: one per
// role it switches on.
func hostVars(comp catalog.Component) []string {
	var out []string
	for _, v := range append([]string{comp.EnableVar}, slices.Collect(maps.Keys(comp.Extra))...) {
		if p, ok := strings.CutSuffix(v, "_enabled"); ok && p != "" {
			out = append(out, p+"_hostname")
		}
	}
	return out
}

type hostResolver struct {
	vars     map[string]any    // vars.yml as interzone writes it
	defaults map[string]string // the playbook's, as raw YAML scalars
}

func (c *Config) hostResolver() hostResolver {
	return hostResolver{vars: renderedVars(c.VarsYAML("", "", "")), defaults: cachedDefaults(c.PlaybookDir)}
}

var reference = regexp.MustCompile(`\{\{\s*([a-z0-9_]+)\s*\}\}`)

// host is the value of name with references filled in, or fallback when
// it isn't a plain hostname.
func (r hostResolver) host(name, fallback string) string {
	v := r.value(name, 0)
	if v == "" || strings.ContainsAny(v, "{}|' /:") {
		return fallback
	}
	return v
}

func (r hostResolver) value(name string, depth int) string {
	if depth > 8 {
		return ""
	}
	var s string
	if v, ok := r.vars[name]; ok {
		switch v := v.(type) {
		case string:
			s = v
		case int:
			s = fmt.Sprint(v) // a port number
		}
	} else if d, ok := r.defaults[name]; ok {
		s = strings.Trim(strings.TrimSpace(d), `"'`)
	}
	return reference.ReplaceAllStringFunc(s, func(m string) string {
		if v := r.value(reference.FindStringSubmatch(m)[1], depth+1); v != "" {
			return v
		}
		return m // left as it was, so host() falls back
	})
}

// The playbook's defaults take a moment to read, so they're kept until the
// playbook's variables change.
var defaultsCache struct {
	sync.Mutex
	dir  string
	when time.Time
	vars map[string]string
}

func cachedDefaults(dir string) map[string]string {
	if dir == "" {
		return nil
	}
	fi, err := os.Stat(filepath.Join(dir, "group_vars", "matrix_servers"))
	if err != nil {
		return nil
	}
	defaultsCache.Lock()
	defer defaultsCache.Unlock()
	if defaultsCache.dir != dir || !defaultsCache.when.Equal(fi.ModTime()) {
		defaultsCache.dir, defaultsCache.when, defaultsCache.vars = dir, fi.ModTime(), PlaybookDefaults(dir)
	}
	return defaultsCache.vars
}
