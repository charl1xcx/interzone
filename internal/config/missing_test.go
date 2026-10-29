package config

import (
	"strings"
	"testing"
)

func TestMissingFlagsInvalidExtraYAML(t *testing.T) {
	c := Default()
	c.Domain = "example.org"
	c.ExtraYAML = "sss\ns\ns"
	got := strings.Join(c.Missing(), "\n")
	if !strings.Contains(got, "aren't valid YAML") {
		t.Fatalf("invalid extra YAML not reported: %q", got)
	}
	c.ExtraYAML = "matrix_synapse_max_upload_size_mb: 200\n"
	if m := c.Missing(); len(m) != 0 {
		t.Fatalf("valid extra YAML reported: %v", m)
	}
}

// The playbook fails on a Synapse password provider next to MAS.
func TestMissingFlagsPasswordProviderWithMAS(t *testing.T) {
	c := Default()
	c.Domain = "example.org"
	c.Components = map[string]bool{"mas": true, "ldap-auth": true}
	c.Values["matrix_synapse_ext_password_provider_ldap_uri"] = "ldap://ldap.example.org"
	c.Values["matrix_synapse_ext_password_provider_ldap_base"] = "dc=example,dc=org"
	if got := strings.Join(c.Missing(), "\n"); !strings.Contains(got, "doesn't work with MAS logins") {
		t.Fatalf("LDAP next to MAS not reported: %q", got)
	}
	c.Components["mas"] = false
	if m := c.Missing(); len(m) != 0 {
		t.Fatalf("LDAP without MAS reported: %v", m)
	}
}
