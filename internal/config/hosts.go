package config

import (
	"os"
	"path/filepath"
	"regexp"
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
	r := c.hostResolver()
	recs := []string{r.host("matrix_server_fqn_matrix", "matrix."+c.Domain)}
	add := func(h string) {
		if h != "" && !contains(recs, h) {
			recs = append(recs, h)
		}
	}
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) {
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
			add(r.host(v, guess))
		}
		if !found {
			add(guess)
		}
	}
	if c.Components["base-domain"] {
		add(c.Domain)
	}
	return recs
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// hostVars are the variables that may hold a component's hostname: one per
// role it switches on.
func hostVars(comp catalog.Component) []string {
	var out []string
	for _, v := range append([]string{comp.EnableVar}, keys(comp.Extra)...) {
		if p, ok := strings.CutSuffix(v, "_enabled"); ok && p != "" {
			out = append(out, p+"_hostname")
		}
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
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
		s, _ = v.(string)
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
