package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// mdadLayout writes a playbook checkout with one server's inventory.
func mdadLayout(t *testing.T, vars, hosts string) string {
	t.Helper()
	dir := t.TempDir()
	hv := filepath.Join(dir, "inventory", "host_vars", "matrix.example.org")
	if err := os.MkdirAll(hv, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(hv, "vars.yml"):            vars,
		filepath.Join(dir, "inventory", "hosts"): hosts,
		filepath.Join(dir, "setup.yml"):          "---\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// sameVars compares two vars.yml files variable by variable.
func sameVars(t *testing.T, want, got string) {
	t.Helper()
	w, g := renderedVars(want), renderedVars(got)
	delete(w, "matrix_playbook_migration_validated_version")
	delete(g, "matrix_playbook_migration_validated_version")
	for k, v := range w {
		if !reflect.DeepEqual(g[k], v) {
			t.Errorf("%s: want %#v, got %#v", k, v, g[k])
		}
	}
	for k, v := range g {
		if _, ok := w[k]; !ok {
			t.Errorf("%s: added as %#v", k, v)
		}
	}
}

// What interzone writes imports back to the same answers.
func TestImportRoundTrip(t *testing.T) {
	c := Default()
	c.Domain = "example.org"
	c.Sources.DockerPackageURL = "https://docker.mirror.example/linux"
	c.Components["telegram"] = true
	c.Components["monitoring"] = true
	c.Components["external-postgres"] = false
	c.Values["matrix_bridge_mautrix_telegram_api_id"] = "12345"
	c.Values["matrix_bridge_mautrix_telegram_api_hash"] = "abcdef"
	c.FillGenerated()
	c.ExtraYAML = "# my own\nmatrix_synapse_max_upload_size_mb: 200"
	c.Overrides["matrix_synapse_workers_enabled"] = "true"
	c.Target = Target{Mode: "ssh", Host: "203.0.113.5", User: "deploy", Port: "2222", BecomePass: "pa ss'"}
	vars := c.VarsYAML("v1", "", "")
	dir := mdadLayout(t, vars, c.HostsINI())

	im, err := ImportMDAD(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := im.Config
	sameVars(t, vars, got.VarsYAML("v1", "", ""))
	if got.GenericSecret != c.GenericSecret || got.PostgresPassword != c.PostgresPassword {
		t.Error("secrets changed")
	}
	if !got.Components["telegram"] || !got.Components["monitoring"] || !got.Components["element"] {
		t.Errorf("components: %v", got.Components)
	}
	if got.Target != c.Target {
		t.Errorf("target: %+v", got.Target)
	}
	if im.PlaybookDir != dir {
		t.Errorf("playbook dir %q", im.PlaybookDir)
	}
	if len(im.Added) > 0 {
		t.Errorf("added %v", im.Added)
	}
	if !slices.Contains(im.Kept, "matrix_synapse_max_upload_size_mb") {
		t.Errorf("kept %v", im.Kept)
	}
	got.FillGenerated()
	sameVars(t, vars, got.VarsYAML("v1", "", "")) // nothing made up afterwards either
}

// A hand-written vars.yml, as MDAD's docs suggest.
func TestImportHandWritten(t *testing.T) {
	vars := `---
# The bare domain name which represents your Matrix identity.
matrix_domain: example.org

matrix_homeserver_implementation: synapse

# A secret used as a base, for generating various other secrets.
matrix_homeserver_generic_secret_key: 'gen-secret'

matrix_playbook_reverse_proxy_type: playbook-managed-traefik

traefik_config_certificatesResolvers_acme_email: 'me@example.org'

postgres_connection_password: 'pg-pass'

matrix_playbook_migration_validated_version: v1.0.0

matrix_bridge_mautrix_whatsapp_enabled: true

# Bigger uploads
matrix_synapse_max_upload_size_mb: 1024
matrix_synapse_configuration_extension_yaml: |
  retention:
    enabled: true
matrix_coturn_enabled: false
`
	hosts := "[matrix_servers]\nmatrix.example.org ansible_host=1.2.3.4 ansible_ssh_user=root\n"
	im, err := ImportMDAD(mdadLayout(t, vars, hosts), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := im.Config
	if c.GenericSecret != "gen-secret" || c.PostgresPassword != "pg-pass" || c.SSL.ACMEEmail != "me@example.org" {
		t.Errorf("answers: %+v", c)
	}
	if !c.Components["whatsapp"] || c.Components["telegram"] {
		t.Errorf("components %v", c.Components)
	}
	if c.Sources.UseRegistry {
		t.Error("a registry mirror the file doesn't use")
	}
	if c.Target.Host != "1.2.3.4" || c.Target.Mode != "ssh" {
		t.Errorf("target %+v", c.Target)
	}
	out := c.VarsYAML("v1.0.0", "", "")
	want := renderedVars(vars)
	got := renderedVars(out)
	for k, v := range want {
		if k != "matrix_playbook_migration_validated_version" && !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s: want %#v, got %#v", k, v, got[k])
		}
	}
	t.Logf("added: %v", im.Added)
	t.Logf("kept: %v; overridden: %v; warnings: %v", im.Kept, im.Override, im.Warnings)
	t.Logf("extra:\n%s", c.ExtraYAML)
	if u, ok := got["matrix_user_creator_users_additional"]; ok && !reflect.DeepEqual(u, []any{}) {
		t.Errorf("would create an admin account: %v", u)
	}
}

// Variables interzone would add are held at the playbook's defaults.
func TestImportKeepsPlaybookDefaults(t *testing.T) {
	vars := "matrix_domain: example.org\nmatrix_homeserver_generic_secret_key: g\npostgres_connection_password: p\n"
	dir := mdadLayout(t, vars, "matrix.example.org ansible_connection=local\n")
	for path, body := range map[string]string{
		"roles/custom/matrix-base/defaults/main.yml": "matrix_homeserver_federation_enabled: true\nmatrix_playbook_reverse_proxy_type: ''\n",
		"roles/galaxy/coturn/defaults/main.yml":      "coturn_enabled: true\n",
		"group_vars/matrix_servers":                  "coturn_enabled: \"{{ jitsi_enabled | bool }}\"\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	im, err := ImportMDAD(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := im.Config
	if c.Overrides["coturn_enabled"] != `"{{ jitsi_enabled | bool }}"` || c.Components["coturn"] {
		t.Errorf("coturn: %q, on %v", c.Overrides["coturn_enabled"], c.Components["coturn"])
	}
	if c.Overrides["matrix_playbook_reverse_proxy_type"] != "''" {
		t.Errorf("reverse proxy: %q", c.Overrides["matrix_playbook_reverse_proxy_type"])
	}
	if slices.Contains(im.Pinned, "matrix_homeserver_federation_enabled") {
		t.Error("pinned a variable already at its default")
	}
	if c.Target.Mode != "local" {
		t.Errorf("target %+v", c.Target)
	}
	t.Logf("pinned %v, added %v", im.Pinned, im.Added)
}

func TestImportRefusesWithoutSecrets(t *testing.T) {
	for name, vars := range map[string]string{
		"generic":  "matrix_domain: example.org\npostgres_connection_password: x\n",
		"postgres": "matrix_domain: example.org\nmatrix_homeserver_generic_secret_key: x\n",
		"vault":    "$ANSIBLE_VAULT;1.1;AES256\n6162\n",
	} {
		if _, err := ImportMDAD(mdadLayout(t, vars, ""), nil); err == nil {
			t.Errorf("%s: imported", name)
		}
	}
}

func TestVarChanges(t *testing.T) {
	got := VarChanges("a: 1\nb: [x, y]\nc: |\n  one\n  two\n", "# hi\nb:\n  - x\n  - y\nc: \"one\\ntwo\\n\"\nd: true\na: 2\n")
	want := []VarChange{{"a", "1", "2"}, {"d", "", "true"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	got = VarChanges("m: {k: [1, 2]}\ns: \"x\\ny\"\n", "")
	if len(got) != 2 || got[0].Old != "{k: [1, 2]}" || got[1].Old != `"x\ny"` {
		t.Errorf("flat: %+v", got)
	}
}
