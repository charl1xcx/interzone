package config

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"interzone/internal/catalog"
)

// ── Import ─────────────────────────────────────────────────────────
//
// A server set up with matrix-docker-ansible-deploy (MDAD) by hand can be
// taken over: its vars.yml is read back into interzone's answers, and
// whatever interzone doesn't model is kept as it was, so the first deploy
// from interzone writes an equivalent vars.yml. Secrets in particular
// must survive: most of MDAD's are derived from the generic secret key,
// and a new Postgres password locks the homeserver out of its database.

// Import is what ImportMDAD found.
type Import struct {
	Config      *Config
	VarsFile    string // the vars.yml read
	HostsFile   string // the inventory hosts file read, if any
	PlaybookDir string // the MDAD checkout around them, if any

	Kept     []string // variables carried over as hand-written extra vars
	Override []string // variables interzone sets too, kept at the file's value
	Pinned   []string // variables interzone would add, held at the playbook's default
	Added    []string // variables interzone adds whose default couldn't be checked
	Warnings []string
}

// legacyPostgresPassword are older names of postgres_connection_password.
var legacyPostgresPassword = []string{"devture_postgres_connection_password", "matrix_postgres_connection_password"}

// ImportMDAD reads an MDAD setup: a playbook checkout, its inventory
// directory, a host_vars directory or a vars.yml. base supplies the
// download settings (mirrors), which vars.yml doesn't hold.
func ImportMDAD(path string, base *Config) (*Import, error) {
	im, err := locate(path)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(im.VarsFile)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimSpace(string(src)), "$ANSIBLE_VAULT") {
		return nil, fmt.Errorf("%s is encrypted with ansible-vault; decrypt a copy (ansible-vault decrypt) and import that", im.VarsFile)
	}
	vars, err := parseVars(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", im.VarsFile, err)
	}
	for _, k := range vars.dupes {
		im.Warnings = append(im.Warnings, k+" is set more than once; the last one counts, as in Ansible")
	}

	c := Default()
	if base != nil {
		c.Sources = base.Sources
	}
	c.AdminUser, c.AdminPassword = "", ""
	c.Components, c.Values = map[string]bool{}, map[string]string{}
	if err := im.mapVars(c, vars); err != nil {
		return nil, err
	}
	if im.HostsFile != "" {
		if err := readHosts(im.HostsFile, c); err != nil {
			im.Warnings = append(im.Warnings, "couldn't read "+im.HostsFile+" ("+err.Error()+"); check the Server step")
		}
	} else {
		im.Warnings = append(im.Warnings, "no inventory/hosts next to vars.yml: set how to reach the server on the Server step")
	}
	if im.PlaybookDir != "" {
		if out, err := exec.Command("git", "-C", im.PlaybookDir, "remote", "get-url", "origin").Output(); err == nil {
			if u := strings.TrimSpace(string(out)); u != "" {
				c.Sources.PlaybookGit = u
			}
		}
	}
	im.Config = c
	ref := im.PlaybookDir
	if ref == "" && base != nil {
		ref = base.PlaybookDir // interzone's own copy knows the same defaults
	}
	im.reconcile(vars, PlaybookDefaults(ref))
	if theirs, _ := vars.str("matrix_playbook_migration_validated_version"); theirs != "" {
		if cur := MigrationVersion(ref); cur != "" && cur != theirs {
			im.Warnings = append(im.Warnings, "your vars.yml confirms the playbook's breaking changes up to "+theirs+
				"; interzone confirms them up to the playbook's current "+cur+" on every update. Read the playbook's CHANGELOG.md between the two before updating")
		}
	}
	im.Config.MarkDeployed() // it runs: the setup mustn't change what can't be
	return im, nil
}

// locate finds vars.yml and the inventory around path.
func locate(path string) (*Import, error) {
	path, err := filepath.Abs(ExpandHome(path))
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	im := &Import{}
	if !st.IsDir() {
		im.VarsFile = path
	} else {
		// The most specific layout that matches wins, so a checkout's
		// examples/vars.yml doesn't count as a server.
		var found []string
		for _, pat := range []string{"inventory/host_vars/*/vars.yml", "host_vars/*/vars.yml", "vars.yml", "*/vars.yml"} {
			if found, _ = filepath.Glob(filepath.Join(path, pat)); len(found) > 0 {
				break
			}
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("no vars.yml in %s or its inventory/host_vars", path)
		case 1:
			im.VarsFile = found[0]
		default:
			for i, f := range found {
				found[i], _ = filepath.Rel(path, filepath.Dir(f))
			}
			return nil, fmt.Errorf("there are several servers in there; add the folder of the one to import:\n  %s", strings.Join(found, "\n  "))
		}
	}
	// …/inventory/host_vars/<host>/vars.yml
	hostDir := filepath.Dir(im.VarsFile)
	if filepath.Base(filepath.Dir(hostDir)) == "host_vars" {
		inv := filepath.Dir(filepath.Dir(hostDir))
		if _, err := os.Stat(filepath.Join(inv, "hosts")); err == nil {
			im.HostsFile = filepath.Join(inv, "hosts")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(inv), "setup.yml")); err == nil && filepath.Base(inv) == "inventory" {
			im.PlaybookDir = filepath.Dir(inv)
		}
	}
	return im, nil
}

// ExpandHome turns a leading ~/ into the home directory.
func ExpandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}

// varsFile is a parsed vars.yml: the top-level variables in file order.
type varsFile struct {
	keys  []string
	node  map[string]*yaml.Node
	value map[string]any // decoded; absent for vault-encrypted values
	text  map[string]string
	dupes []string
}

func parseVars(src string) (*varsFile, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, fmt.Errorf("not valid YAML (%s)", yamlError(err))
	}
	v := &varsFile{node: map[string]*yaml.Node{}, value: map[string]any{}, text: map[string]string{}}
	if len(doc.Content) == 0 {
		return v, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected variables at the top level")
	}
	lines := strings.Split(src, "\n")
	type span struct {
		key        string
		start, end int // 0-based lines, end exclusive
	}
	var spans []span
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		spans = append(spans, span{key: k.Value, start: k.Line - 1})
	}
	for i := range spans {
		end := len(lines)
		if i+1 < len(spans) {
			end = spans[i+1].start
			// Comments right above the next variable belong to it.
			for end-1 > spans[i].start && strings.HasPrefix(strings.TrimSpace(lines[end-1]), "#") {
				end--
			}
			spans[i+1].start = end
		}
		spans[i].end = end
	}
	if len(spans) > 0 {
		// Comments at the top, after any document marker, go with the first variable.
		spans[0].start = 0
		for i := 0; i < spans[0].start || i < m.Content[0].Line-1; i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				spans[0].start = i + 1
			}
		}
	}
	for i, s := range spans {
		k, val := m.Content[2*i], m.Content[2*i+1]
		if _, seen := v.node[k.Value]; seen {
			v.dupes = append(v.dupes, k.Value)
			v.keys = slices.DeleteFunc(v.keys, func(key string) bool { return key == k.Value })
		}
		v.keys = append(v.keys, k.Value)
		v.node[k.Value] = val
		var body []string
		for _, l := range lines[s.start:s.end] {
			if t := strings.TrimSpace(l); t == "---" || t == "..." {
				continue
			}
			body = append(body, l)
		}
		v.text[k.Value] = strings.TrimRight(strings.Join(body, "\n"), "\n ")
		delete(v.value, k.Value)
		if !vaulted(val) {
			var x any
			if err := val.Decode(&x); err == nil {
				v.value[k.Value] = x
			}
		}
	}
	return v, nil
}

func vaulted(n *yaml.Node) bool { return n.Tag == "!vault" }

// str returns a variable as a string, if it is a plain scalar.
func (v *varsFile) str(key string) (string, bool) {
	n, ok := v.node[key]
	if !ok || vaulted(n) || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

func (v *varsFile) boolean(key string) (on, ok bool) {
	x, ok := v.value[key].(bool)
	return x, ok
}

// valueYAML renders a variable's value as YAML, for Overrides.
func (v *varsFile) valueYAML(key string) string {
	b, err := yaml.Marshal(v.node[key])
	if err != nil {
		return v.text[key]
	}
	return strings.TrimRight(string(b), "\n")
}

// mapVars fills c from the variables interzone has answers for.
func (im *Import) mapVars(c *Config, v *varsFile) error {
	domain, ok := v.str("matrix_domain")
	if !ok || domain == "" || strings.Contains(domain, "{{") {
		return fmt.Errorf("%s doesn't set matrix_domain to a plain domain name", im.VarsFile)
	}
	c.Domain = domain

	c.Homeserver = "synapse"
	if hs, ok := v.str("matrix_homeserver_implementation"); ok {
		known := false
		for _, h := range catalog.Homeservers {
			known = known || h.ID == hs
		}
		if !known {
			return fmt.Errorf("matrix_homeserver_implementation %q isn't one interzone knows", hs)
		}
		c.Homeserver = hs
	}

	// The generic secret seeds most of MDAD's other secrets, and the
	// Postgres password guards an existing database: neither can be made up.
	if _, ok := v.node["matrix_homeserver_generic_secret_key"]; !ok {
		return fmt.Errorf("%s has no matrix_homeserver_generic_secret_key; interzone would have to make a new one, which changes every secret derived from it", im.VarsFile)
	}
	c.GenericSecret, _ = v.str("matrix_homeserver_generic_secret_key")
	external := false
	if on, ok := v.boolean("postgres_enabled"); ok && !on {
		external = true
	}
	c.PostgresPassword = ""
	if _, ok := v.node["postgres_connection_password"]; ok {
		c.PostgresPassword, _ = v.str("postgres_connection_password")
	} else {
		for _, k := range legacyPostgresPassword {
			if p, ok := v.str(k); ok {
				c.PostgresPassword = p
				im.Warnings = append(im.Warnings, k+" is the old name of postgres_connection_password; both are written")
				break
			}
		}
		if c.PostgresPassword == "" && !external {
			return fmt.Errorf("%s has no postgres_connection_password; a new one would lock the homeserver out of its database", im.VarsFile)
		}
	}

	c.IPv6, _ = v.boolean("devture_systemd_docker_base_ipv6_enabled")
	c.Federation = true
	if on, ok := v.boolean("matrix_homeserver_federation_enabled"); ok {
		c.Federation = on
	}

	c.SSL = SSL{Mode: "letsencrypt"}
	if on, ok := v.boolean("traefik_config_entrypoint_web_secure_enabled"); ok && !on {
		c.SSL.Mode = "none"
	}
	c.SSL.ACMEEmail, _ = v.str("traefik_config_certificatesResolvers_acme_email")
	if on, ok := v.boolean("traefik_config_certificatesResolvers_acme_enabled"); ok && !on && c.SSL.Mode != "none" {
		im.Warnings = append(im.Warnings, "certificates are set up by hand (Let's Encrypt is off); that setup is kept as extra vars, so leave the HTTPS setting alone")
	}

	prefix, ok := v.str("matrix_container_global_registry_prefix_override")
	c.Sources.UseRegistry = ok && prefix != ""
	if c.Sources.UseRegistry {
		c.Sources.RegistryPrefix = prefix
	}
	c.Sources.DockerPackageURL, _ = v.str("docker_repo_url")

	switch c.Homeserver {
	case "synapse", "dendrite":
		if users, ok := v.value["matrix_user_creator_users_additional"].([]any); ok {
			for _, u := range users {
				m, _ := u.(map[string]any)
				if t, _ := m["initial_type"].(string); t == "admin" {
					c.AdminUser, _ = m["username"].(string)
					c.AdminPassword, _ = m["initial_password"].(string)
					break
				}
			}
		} else {
			// Don't have the server create an admin account it never had.
			c.Overrides["matrix_user_creator_users_additional"] = "[]"
		}
		if c.Homeserver == "synapse" {
			// The token itself lives in MAS's database; a new one is made.
			c.Signup, _ = v.boolean("matrix_authentication_service_config_account_password_registration_enabled")
		}
	case "continuwuity", "tuwunel":
		on, _ := v.boolean("matrix_" + c.Homeserver + "_config_allow_registration")
		c.CloseRegistration = !on
		c.AdminPassword, _ = v.str("matrix_" + c.Homeserver + "_config_registration_token")
	case "conduit":
		on, _ := v.boolean("matrix_conduit_allow_registration")
		c.CloseRegistration = !on
	}

	for _, comp := range catalog.Components {
		if comp.SynapseOnly && c.Homeserver != "synapse" {
			continue
		}
		on := comp.DefaultOn
		if comp.EnableVar != "" {
			if x, ok := v.boolean(comp.EnableVar); ok {
				on = x
			} else if _, set := v.node[comp.EnableVar]; set {
				on = true // a template: kept as written, see reconcile
			}
		} else {
			on = len(comp.Extra) > 0
			for k, want := range comp.Extra {
				var w any
				_ = yaml.Unmarshal([]byte(want), &w)
				on = on && reflect.DeepEqual(v.value[k], w)
			}
		}
		if !on {
			continue
		}
		c.Components[comp.ID] = true
		for _, f := range comp.Fields {
			// An empty value is written as nothing, and stops FillGenerated
			// from inventing secrets the server never had.
			c.Values[f.Var] = fieldValue(v, f)
		}
		if h, ok := v.str(comp.HostVar()); ok && ValidHost(h) {
			c.Values[comp.HostVar()] = h
		}
	}
	return nil
}

// fieldValue is a variable as the component forms hold it.
func fieldValue(v *varsFile, f catalog.Field) string {
	n, ok := v.node[f.Var]
	if !ok || vaulted(n) {
		return ""
	}
	switch f.Kind {
	case catalog.Bool:
		if on, ok := v.boolean(f.Var); ok {
			return fmt.Sprint(on)
		}
	case catalog.List:
		if items, ok := v.value[f.Var].([]any); ok {
			var s []string
			for _, it := range items {
				str, ok := it.(string)
				if !ok || strings.Contains(str, ",") {
					return "" // not representable; reconcile keeps it as written
				}
				s = append(s, str)
			}
			return strings.Join(s, ",")
		}
	case catalog.YAML:
		return v.valueYAML(f.Var)
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// reconcile makes the vars.yml interzone writes match the imported one:
// a variable interzone writes differently is pinned in Overrides, one it
// doesn't write at all goes to the extra vars as it was written.
//
// A variable interzone would add is held at the playbook's own default
// (from defaults, raw YAML by name), so turning a component "on by
// default" in interzone's catalog can't switch on something the server
// never ran.
func (im *Import) reconcile(v *varsFile, defaults map[string]string) {
	c := im.Config
	mv, _ := v.str("matrix_playbook_migration_validated_version")
	ours := renderedVars(c.VarsYAML(mv, "", ""))
	var extra []string
	for _, k := range v.keys {
		if k == "matrix_playbook_migration_validated_version" {
			continue // interzone keeps it at the playbook's current value
		}
		got, written := ours[k]
		switch {
		case !written:
			extra = append(extra, v.text[k])
			im.Kept = append(im.Kept, k)
		case vaulted(v.node[k]) || !reflect.DeepEqual(got, v.value[k]):
			c.Overrides[k] = v.valueYAML(k)
			im.Override = append(im.Override, k)
		}
	}
	c.ExtraYAML = strings.Join(extra, "\n")
	for k, got := range renderedVars(c.VarsYAML(mv, "", "")) {
		if _, ok := v.node[k]; ok || k == "matrix_playbook_migration_validated_version" {
			continue
		}
		raw, known := defaults[k]
		if !known {
			im.Added = append(im.Added, k)
			continue
		}
		var def any
		if yaml.Unmarshal([]byte(raw), &def) == nil && reflect.DeepEqual(got, def) {
			continue // what the playbook does anyway
		}
		c.Overrides[k] = raw
		im.Pinned = append(im.Pinned, k)
		for _, comp := range catalog.Components {
			if comp.EnableVar == k && def != true {
				c.Components[comp.ID] = false
			}
		}
	}
	sort.Strings(im.Added)
	sort.Strings(im.Pinned)
}

// PlaybookDefaults maps every variable the playbook defines to the value
// that applies when vars.yml doesn't set it.
func PlaybookDefaults(dir string) map[string]string {
	out := map[string]string{}
	if dir == "" {
		return out
	}
	roles, _ := Roles(dir)
	for _, r := range roles {
		for _, v := range r.Vars {
			out[v.Name] = v.Effective()
		}
	}
	return out
}

func renderedVars(src string) map[string]any {
	m := map[string]any{}
	_ = yaml.Unmarshal([]byte(src), &m)
	return m
}

// readHosts takes how to reach the server from an MDAD inventory/hosts.
func readHosts(path string, c *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var host string
	var opts map[string]string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' || line[0] == '[' {
			continue
		}
		fields := splitHostLine(line)
		o := map[string]string{}
		for _, f := range fields[1:] {
			if k, val, ok := strings.Cut(f, "="); ok {
				o[k] = unquote(val)
			}
		}
		if host == "" || fields[0] == c.HostName() {
			host, opts = fields[0], o
		}
		if fields[0] == c.HostName() {
			break
		}
	}
	if host == "" {
		return fmt.Errorf("no host in it")
	}
	t := Target{Mode: "ssh", Host: host, User: "root", Port: "22"}
	if opts["ansible_connection"] == "local" {
		t.Mode, t.Host = "local", ""
	}
	if h := opts["ansible_host"]; h != "" {
		t.Host = h
	}
	for _, k := range []string{"ansible_ssh_user", "ansible_user"} {
		if u := opts[k]; u != "" {
			t.User = u
			break
		}
	}
	for _, k := range []string{"ansible_port", "ansible_ssh_port"} {
		if p := opts[k]; p != "" {
			t.Port = p
			break
		}
	}
	t.SSHKey = opts["ansible_ssh_private_key_file"]
	for _, k := range []string{"ansible_become_password", "ansible_become_pass", "ansible_sudo_pass"} {
		if p := opts[k]; p != "" {
			t.BecomePass = p
			break
		}
	}
	c.Target = t
	return nil
}

// splitHostLine splits an INI inventory line on spaces outside quotes.
func splitHostLine(line string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		case r == '#':
			if cur.Len() == 0 {
				return out // a comment
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// unquote undoes the quoting of an inventory value, including the
// doubled ” that HostsINI writes for a quote inside single quotes.
func unquote(s string) string {
	switch {
	case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		return s[1 : len(s)-1]
	}
	return s
}

// SuggestName picks a server name for an import: the first server if it
// was never set up, else one made from the domain.
func SuggestName(domain string) string {
	if c, err := LoadFile(ProfilePath(DefaultProfile)); err != nil || c.Domain == "" {
		return DefaultProfile
	}
	base := strings.Trim(regexp.MustCompile(`[^a-z0-9.-]+`).ReplaceAllString(strings.ToLower(domain), "-"), "-.")
	if len(base) > 36 || base == "" {
		base = strings.Trim(base[:min(len(base), 36)], "-.")
		if base == "" {
			base = "imported"
		}
	}
	name := base
	for i := 2; ; i++ {
		if _, err := os.Stat(ProfilePath(name)); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// Save stores the import as server name; making it the active one is up
// to the caller. A playbook checkout is copied for interzone to work in, so the original
// stays exactly as it was; the imported vars.yml and hosts are also kept
// under imported/<name> for reference.
func (im *Import) Save(name string) error {
	if err := ValidProfileName(name); err != nil {
		return err
	}
	if c, err := LoadFile(ProfilePath(name)); err == nil && (name != DefaultProfile || c.Domain != "") {
		return fmt.Errorf("there is already a server called %s", name)
	}
	c := im.Config
	c.PlaybookDir = filepath.Join(DataDir(), "servers", name, "matrix-docker-ansible-deploy")
	if name == DefaultProfile {
		c.PlaybookDir = filepath.Join(DataDir(), "matrix-docker-ansible-deploy")
	}
	if im.PlaybookDir != "" {
		if _, err := os.Stat(c.PlaybookDir); err == nil {
			return fmt.Errorf("%s already exists; move it away first", c.PlaybookDir)
		}
		if err := im.copyPlaybook(c); err != nil {
			return err
		}
	}
	return im.keep(name, c)
}

// copyPlaybook copies the imported checkout to c.PlaybookDir.
func (im *Import) copyPlaybook(c *Config) error {
	if err := os.MkdirAll(filepath.Dir(c.PlaybookDir), 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("cp", "-a", im.PlaybookDir, c.PlaybookDir).CombinedOutput(); err != nil {
		_ = os.RemoveAll(c.PlaybookDir)
		return fmt.Errorf("copying the playbook: %s", strings.TrimSpace(string(out)))
	}
	// interzone looks for the inventory under matrix.<domain>.
	want := filepath.Join(c.PlaybookDir, "inventory", "host_vars", c.HostName(), "vars.yml")
	if _, err := os.Stat(want); os.IsNotExist(err) {
		b, err := os.ReadFile(im.VarsFile)
		if err == nil {
			err = os.MkdirAll(filepath.Dir(want), 0o700)
		}
		if err == nil {
			err = os.WriteFile(want, b, 0o600)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Destination is where the import goes: the server called name, or with
// no name the saved server for the same domain, else a new one named
// after the domain. update reports that an existing server is replaced.
func (im *Import) Destination(name string) (dest string, update bool, err error) {
	if name == "" {
		if ex := im.Existing(); ex != "" {
			return ex, true, nil
		}
		return SuggestName(im.Config.Domain), false, nil
	}
	if err := ValidProfileName(name); err != nil {
		return "", false, err
	}
	c, err := LoadFile(ProfilePath(name))
	switch {
	case err != nil || c.Domain == "":
		return name, false, nil
	case !strings.EqualFold(c.Domain, im.Config.Domain):
		return "", false, fmt.Errorf("there is already a server called %s, for %s", name, c.Domain)
	}
	return name, true, nil
}

// Existing names the saved server for the same domain, if there is one:
// importing that vars.yml again updates it instead of adding a server.
func (im *Import) Existing() string {
	for _, p := range Profiles() {
		if p.Domain != "" && strings.EqualFold(p.Domain, im.Config.Domain) {
			return p.Name
		}
	}
	return ""
}

// Update replaces the answers of the saved server name with the import's.
// What vars.yml doesn't hold stays as it was: the playbook copy, where
// downloads come from, scheduled backups, a held rollback and interzone's
// admin session, plus how the server is reached when there's no inventory.
// The answers from before are kept next to the server's file.
func (im *Import) Update(name string) error {
	path := ProfilePath(name)
	old, err := LoadFile(path)
	if err != nil {
		return err
	}
	c := im.Config
	c.PlaybookDir, c.Sources, c.Backups, c.PinnedCommit = old.PlaybookDir, old.Sources, old.Backups, old.PinnedCommit
	if im.HostsFile == "" {
		c.Target = old.Target
	}
	if c.AdminUser == "" {
		c.AdminUser, c.AdminPassword = old.AdminUser, old.AdminPassword
	}
	if c.AdminUser == old.AdminUser {
		c.AdminToken = old.AdminToken
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".before-import", b, 0o600); err != nil {
			return err
		}
	}
	if _, err := os.Stat(c.PlaybookDir); os.IsNotExist(err) && im.PlaybookDir != "" {
		if err := im.copyPlaybook(c); err != nil {
			return err
		}
	}
	return im.keep(name, c)
}

// keep saves c as server name, with the imported files under imported/name
// for reference.
func (im *Import) keep(name string, c *Config) error {
	keep := filepath.Join(Dir(), "imported", name)
	if err := os.MkdirAll(keep, 0o700); err != nil {
		return err
	}
	for _, f := range []string{im.VarsFile, im.HostsFile} {
		if b, err := os.ReadFile(f); err == nil {
			_ = os.WriteFile(filepath.Join(keep, filepath.Base(f)), b, 0o600)
		}
	}
	return c.SaveFile(ProfilePath(name))
}

// ComponentNames lists the components the import turned on.
func (im *Import) ComponentNames() []string {
	var out []string
	for _, comp := range catalog.Components {
		if im.Config.Enabled(comp) {
			out = append(out, comp.Name)
		}
	}
	return out
}

// Where says how the server is reached, for a summary.
func (im *Import) Where() string {
	if t := im.Config.Target; t.Mode == "ssh" {
		return t.User + "@" + t.Host
	}
	return "this machine"
}

// ImportedVars is the vars.yml a server was imported from, if it was.
func ImportedVars(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(Dir(), "imported", name, "vars.yml"))
	return string(b), err == nil
}

// VarChange is one variable that differs between two vars.yml files.
type VarChange struct {
	Name     string
	Old, New string // one-line YAML; empty when absent
}

// VarChanges compares two vars.yml files variable by variable, ignoring
// order, formatting and comments.
func VarChanges(old, cur string) []VarChange {
	o, n := renderedVars(old), renderedVars(cur)
	flat := func(m map[string]any, k string) string {
		v, ok := m[k]
		if !ok {
			return ""
		}
		var node yaml.Node
		if node.Encode(v) == nil {
			flow(&node)
			if b, err := yaml.Marshal(&node); err == nil {
				return strings.TrimSpace(string(b))
			}
		}
		return fmt.Sprint(v)
	}
	all := maps.Clone(o)
	maps.Copy(all, n)
	var out []VarChange
	for _, k := range slices.Sorted(maps.Keys(all)) {
		if !reflect.DeepEqual(o[k], n[k]) {
			out = append(out, VarChange{k, flat(o, k), flat(n, k)})
		}
	}
	return out
}

// flow makes a node render on one line.
func flow(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style = yaml.FlowStyle
	}
	if n.Kind == yaml.ScalarNode && strings.Contains(n.Value, "\n") {
		n.Style = yaml.DoubleQuotedStyle
	}
	for _, c := range n.Content {
		flow(c)
	}
}

// SameYAML reports whether two YAML values are equal; an empty one never is.
func SameYAML(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	var x, y any
	return yaml.Unmarshal([]byte(a), &x) == nil && yaml.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}
