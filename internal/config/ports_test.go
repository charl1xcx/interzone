package config

import (
	"strings"
	"testing"
)

func TestPorts(t *testing.T) {
	list := func(c *Config) string {
		var s []string
		for _, p := range c.Ports() {
			s = append(s, p.Port)
		}
		return strings.Join(s, " | ")
	}
	c := Default()
	c.Homeserver, c.Federation = "synapse", false
	c.Components = map[string]bool{}
	if got := list(c); got != "80/tcp, 443/tcp+udp" {
		t.Errorf("bare server: %s", got)
	}
	c.Components = map[string]bool{"uvs": true, "coturn": true, "postmoogle": true}
	got := list(c)
	for _, want := range []string{"8448/tcp+udp", "3478, 5349/tcp+udp", "49152-49172/udp", "25/tcp, 587/tcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	c.Homeserver, c.Federation = "conduit", true // federation on 443
	if strings.Contains(list(c), "8448") {
		t.Errorf("conduit opens 8448: %s", list(c))
	}
}
