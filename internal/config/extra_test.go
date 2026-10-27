package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExtraGroups(t *testing.T) {
	c := Default()
	c.Domain, c.Homeserver = "example.org", "synapse"
	// A playbook with a few roles; the rest is left to the names.
	c.PlaybookDir = t.TempDir()
	for role, vars := range map[string]string{
		"galaxy/docker":                        "docker_daemon_options: {}\n",
		"galaxy/livekit_server":                "livekit_server_config_rtc_use_external_ip: false\n",
		"custom/matrix-authentication-service": "matrix_authentication_service_enabled: false\nmatrix_authentication_service_config_account_password_registration_enabled: false\n",
	} {
		dir := filepath.Join(c.PlaybookDir, "roles", role, "defaults")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.yml"), []byte(vars), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.Components["element"], c.Components["mas"], c.Components["element-call"] = true, true, true
	c.ExtraYAML = `docker_daemon_options:
  log-driver: local
# the old one:
#matrix_authentication_service_config_account_registration_token_required: true
matrix_authentication_service_config_account_password_registration_enabled: true
matrix_synapse_configuration_extension_yaml: |
  retention:
    enabled: true

matrix_client_element_hostname: chat.example.org
cinny_hostname: cinny.other.org
livekit_server_config_rtc_use_external_ip: true
traefik_config_accessLog_enabled: true
matrix_element_call_hostname: call.example.org`
	got := map[string]string{}
	var order []string
	for _, g := range c.ExtraGroups() {
		got[g.Name] = g.Text
		order = append(order, g.Name)
	}
	if order[0] != "Synapse" || order[len(order)-1] != "Other" || slices.Contains(order, "Coturn TURN server") {
		t.Errorf("order %v", order)
	}
	want := map[string][]string{
		"docker":                        {"docker_daemon_options:\n  log-driver: local"},
		"livekit_server":                {"livekit_server_config_rtc_use_external_ip"},
		"Other":                         {"traefik_config_accessLog_enabled"},
		"Matrix Authentication Service": {"# the old one:", "#matrix_authentication_service_config_account_registration_token_required", "password_registration_enabled: true"},
		"Synapse":                       {"extension_yaml: |\n  retention:\n    enabled: true"},
		"Element Web":                   {"matrix_client_element_hostname"},
		"Cinny":                         {"cinny_hostname"},
		"Element Call":                  {"matrix_element_call_hostname"},
	}
	for name, parts := range want {
		for _, p := range parts {
			if !strings.Contains(got[name], p) {
				t.Errorf("%s lacks %q:\n%s", name, p, got[name])
			}
		}
	}
	if strings.Contains(got["Other"], "#matrix_authentication") {
		t.Error("a comment stayed with the key above it")
	}
	// Nothing is lost on the way back.
	var before, after map[string]any
	_ = yaml.Unmarshal([]byte(c.ExtraYAML), &before)
	if err := yaml.Unmarshal([]byte(JoinExtra(c.ExtraGroups())), &after); err != nil || len(after) != len(before) {
		t.Errorf("round trip: %v, %d keys of %d", err, len(after), len(before))
	}
}
