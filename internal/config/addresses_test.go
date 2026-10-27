package config

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAddresses(t *testing.T) {
	c := Default()
	c.Domain, c.Homeserver = "example.org", "synapse"
	c.Components["element"], c.Components["cinny"] = true, true
	c.Values["matrix_client_element_hostname"] = "chat.other.net"
	y := c.VarsYAML("v1", "", "")
	if !strings.Contains(y, "matrix_client_element_hostname: 'chat.other.net'") {
		t.Error("address not written")
	}
	if strings.Contains(y, "cinny_hostname") {
		t.Error("an empty address is written; the playbook's default should stand")
	}
	recs := c.DNSRecords()
	if !slices.Contains(recs, "chat.other.net") || slices.Contains(recs, "element.example.org") {
		t.Errorf("DNS records %v", recs)
	}
	c.Values["matrix_client_element_hostname"] = "chat.other.net/x"
	if !slices.ContainsFunc(c.Missing(), func(s string) bool { return strings.Contains(s, "isn't a hostname") }) {
		t.Error("a bad address passes")
	}
}

// A key in the hand-written extra vars is written once, as written there:
// a duplicate made the whole file unreadable to the DNS check.
func TestExtraVarsWin(t *testing.T) {
	c := Default()
	c.Domain, c.Homeserver = "example.org", "synapse"
	c.Components["element"], c.Components["mas"], c.Signup = true, true, true
	c.ExtraYAML = "matrix_authentication_service_config_account_password_registration_enabled: false\nmatrix_client_element_hostname: chat.other.net"
	y := c.VarsYAML("v1", "", "")
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	if m["matrix_authentication_service_config_account_password_registration_enabled"] != false {
		t.Error("the hand-written value lost")
	}
	if !slices.Contains(c.DNSRecords(), "chat.other.net") {
		t.Errorf("DNS records %v", c.DNSRecords())
	}
}

func TestValidHost(t *testing.T) {
	for h, ok := range map[string]bool{"chat.example.org": true, "a.b-c.io": true, "example": false, "Chat.example.org": false, "-a.example.org": false, "a..b": false, "a.b/c": false, "1.2.3.4": false} {
		if ValidHost(h) != ok {
			t.Errorf("%q: %v", h, !ok)
		}
	}
}
