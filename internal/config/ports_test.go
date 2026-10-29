package config

import (
	"strings"
	"testing"
)

func TestPorts(t *testing.T) {
	list := func(c *Config) string {
		var s []string
		for _, p := range c.Ports() {
			s = append(s, p.String())
		}
		return strings.Join(s, " | ")
	}
	c := Default()
	c.PlaybookDir = t.TempDir()
	c.Homeserver, c.Federation = "synapse", false
	c.Components = map[string]bool{}
	if got := list(c); got != "80, 443/tcp; 443/udp" {
		t.Errorf("bare server: %s", got)
	}
	c.Components = map[string]bool{"uvs": true, "coturn": true, "postmoogle": true}
	got := list(c)
	for _, want := range []string{"8448/tcp; 8448/udp", "3478, 5349/tcp; 3478, 5349, 49152-49172/udp", "25, 587/tcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	// ports moved in extra vars are followed
	c.ExtraYAML = "coturn_turn_udp_max_port: 49200\nmatrix_bridge_postmoogle_smtp_host_bind_port: '0.0.0.0:2525'\n"
	if got := list(c); !strings.Contains(got, "49152-49200/udp") || !strings.Contains(got, "2525, 587/tcp") {
		t.Errorf("moved ports not followed: %s", got)
	}
	c.Homeserver, c.Federation = "conduit", true // federation on 443
	if strings.Contains(list(c), "8448") {
		t.Errorf("conduit opens 8448: %s", list(c))
	}
}
