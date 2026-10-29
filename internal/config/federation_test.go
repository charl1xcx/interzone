package config

import (
	"strings"
	"testing"
)

func TestFederationLists(t *testing.T) {
	c := Default()
	c.Domain, c.Homeserver = "example.org", "synapse"
	c.FederateOnly, c.FederateNever = []string{"matrix.org", "friends.example"}, []string{"bad.example"}
	y := c.VarsYAML("", "", "")
	if !strings.Contains(y, `matrix_synapse_federation_domain_whitelist: ["matrix.org","friends.example"]`) || strings.Contains(y, "bad.example") {
		t.Errorf("synapse:\n%s", y)
	}
	c.Homeserver = "tuwunel"
	y = c.VarsYAML("", "", "")
	if !strings.Contains(y, `matrix_tuwunel_config_forbidden_remote_server_names: ["^bad\\.example$"]`) {
		t.Errorf("tuwunel:\n%s", y)
	}
	c.Federation = false
	if strings.Contains(c.VarsYAML("", "", ""), "remote_server_names") {
		t.Errorf("lists written with federation off")
	}
}
