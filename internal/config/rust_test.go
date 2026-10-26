package config

import (
	"strings"
	"testing"
)

func TestRustFederationOn443(t *testing.T) {
	for _, hs := range []string{"conduit", "continuwuity", "tuwunel", "synapse"} {
		c := Default()
		c.Domain, c.Homeserver = "example.org", hs
		y := c.VarsYAML("v1", "", "")
		on := strings.Contains(y, "matrix_federation_public_port: 443\n") &&
			strings.Contains(y, "matrix_"+hs+"_container_labels_public_federation_api_enabled: false\n")
		if on == (hs == "synapse") {
			t.Errorf("%s: 443 %v", hs, on)
		}
		if sso := strings.Contains(y, "TUWUNEL_OIDC_NATIVE_AUTH=true"); sso != (hs == "tuwunel") {
			t.Errorf("%s: native login %v", hs, sso)
		}
	}
	c := Default()
	c.Domain, c.Homeserver, c.SSL.Mode = "example.org", "tuwunel", "none"
	if strings.Contains(c.VarsYAML("v1", "", ""), "OIDC_NATIVE") {
		t.Error("native login without HTTPS, where it has no well-known to work from")
	}
}
