package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseDefaults(t *testing.T) {
	text := `---
# The image to run.
# Second line.
matrix_synapse_container_image: "{{ matrix_synapse_container_image_registry_prefix }}matrixdotorg/synapse:{{ matrix_synapse_version }}"

matrix_synapse_version: v1.120.0 # pinned

# A list.
matrix_synapse_extra_listeners:
  - port: 9000
    type: http

matrix_synapse_block: |
  line one
  line two
`
	vars := ParseDefaults(text)
	got := map[string]RoleVar{}
	for _, v := range vars {
		got[v.Name] = v
	}
	if len(got) != 4 {
		t.Fatalf("got %d vars: %+v", len(got), vars)
	}
	img := got["matrix_synapse_container_image"]
	if !img.Derived || !strings.Contains(img.Help, "Second line") {
		t.Errorf("image var: %+v", img)
	}
	if v := got["matrix_synapse_version"]; v.Default != "v1.120.0" || v.Derived {
		t.Errorf("version: %q (derived %v)", v.Default, v.Derived)
	}
	l := got["matrix_synapse_extra_listeners"]
	var parsed []map[string]any
	if err := yaml.Unmarshal([]byte(l.Default), &parsed); err != nil || len(parsed) != 1 {
		t.Errorf("list default %q doesn't parse: %v", l.Default, err)
	}
}

func TestOverridesRendered(t *testing.T) {
	c := Default()
	c.Domain = "example.com"
	c.Homeserver = "synapse"
	c.Overrides = map[string]string{
		"matrix_synapse_max_upload_size_mb": "200",
		"matrix_synapse_extra_listeners":    "- port: 9000\n  type: http",
	}
	out := c.VarsYAML("", "", "")
	var vars map[string]any
	if err := yaml.Unmarshal([]byte(out), &vars); err != nil {
		t.Fatalf("rendered vars aren't YAML: %v\n%s", err, out)
	}
	if vars["matrix_synapse_max_upload_size_mb"] != 200 {
		t.Errorf("upload size = %v", vars["matrix_synapse_max_upload_size_mb"])
	}
	if l, ok := vars["matrix_synapse_extra_listeners"].([]any); !ok || len(l) != 1 {
		t.Errorf("listeners = %#v", vars["matrix_synapse_extra_listeners"])
	}
	if strings.Count(out, "matrix_synapse_max_upload_size_mb:") != 1 {
		t.Errorf("override written more than once:\n%s", out)
	}
}

func TestProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("INTERZONE_SERVER", "")
	if Active() != DefaultProfile {
		t.Fatalf("active = %q", Active())
	}
	for _, bad := range []string{"", "../x", "Has Space", "UPPER"} {
		if ValidProfileName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	base := Default()
	base.Domain = "one.example"
	if err := base.SaveFile(ProfilePath(DefaultProfile)); err != nil {
		t.Fatal(err)
	}
	p := NewProfile("work", base)
	p.Domain = "two.example"
	if err := p.SaveFile(ProfilePath("work")); err != nil {
		t.Fatal(err)
	}
	if p.PlaybookDir == base.PlaybookDir || !strings.Contains(p.PlaybookDir, filepath.Join("servers", "work")) {
		t.Errorf("new profile shares or misplaces the playbook: %s", p.PlaybookDir)
	}
	if err := SetActive("work"); err != nil {
		t.Fatal(err)
	}
	if Path() != ProfilePath("work") {
		t.Errorf("Path = %s", Path())
	}
	if c, ok := Load(); !ok || c.Domain != "two.example" {
		t.Errorf("loaded %+v", c)
	}
	t.Setenv("INTERZONE_SERVER", DefaultProfile)
	if Active() != DefaultProfile {
		t.Errorf("INTERZONE_SERVER ignored: %q", Active())
	}
	if n := len(Profiles()); n != 2 {
		t.Errorf("%d profiles", n)
	}
}

func TestStripComment(t *testing.T) {
	for in, want := range map[string]string{
		` v1.2 # pinned`:         `v1.2`,
		` "a # b"  # c`:          `"a # b"`,
		` 'it''s' # x`:           `'it''s'`,
		` http://x/#frag`:        ` http://x/#frag`,
		` "{{ a }}"  # noqa var`: `"{{ a }}"`,
		` plain`:                 ` plain`,
	} {
		if got := stripComment(in); got != want {
			t.Errorf("stripComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("roles/custom/matrix-synapse/defaults/main.yml", "matrix_synapse_enabled: true\nmatrix_synapse_x: 1\n")
	write("roles/custom/matrix-empty/tasks/main.yml", "- debug:\n")
	write("roles/galaxy/ntfy/defaults/main.yml", "# Enable it.\nntfy_enabled: true\n")
	write("roles/galaxy/split/defaults/main/a.yml", "split_a: 1\n")
	write("roles/galaxy/split/defaults/main/b.yml", "split_b: 2\n")
	write("group_vars/matrix_servers", "ntfy_enabled: false\nmatrix_synapse_enabled: \"{{ x == 'synapse' }}\"\nmatrix_playbook_only: yes\n")

	roles, err := Roles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]Role{}
	for _, r := range roles {
		names = append(names, r.Name)
		byName[r.Name] = r
	}
	if got := strings.Join(names, " "); got != "playbook-wide matrix-synapse ntfy split" {
		t.Fatalf("roles = %s", got)
	}
	n := byName["ntfy"].Vars[0]
	if !byName["ntfy"].Galaxy || !n.PlaybookSet || n.Effective() != "false" || n.Default != "true" || n.Help != "Enable it." {
		t.Errorf("ntfy_enabled = %+v", n)
	}
	if v := byName["matrix-synapse"].Vars[0]; !v.Derived {
		t.Errorf("computed playbook value not marked derived: %+v", v)
	}
	if len(byName["split"].Vars) != 2 {
		t.Errorf("split defaults: %+v", byName["split"].Vars)
	}
	if w := byName["playbook-wide"].Vars; len(w) != 1 || w[0].Name != "matrix_playbook_only" {
		t.Errorf("playbook-wide = %+v", w)
	}
}
