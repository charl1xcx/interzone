package config

import "testing"

func TestEstimate(t *testing.T) {
	c := Default()
	c.Homeserver, c.Components = "synapse", map[string]bool{}
	base, baseDisk := c.Estimate()
	if base != 1024+64+256 || baseDisk < 5 {
		t.Errorf("bare Synapse: %d MB, %d GB", base, baseDisk)
	}
	c.Components = map[string]bool{"jitsi": true, "telegram": true, "element": true, "bridges-e2ee": true}
	if mem, _ := c.Estimate(); mem != base+1536+128+16 {
		t.Errorf("with Jitsi, a bridge and Element: %d MB", mem)
	}
	c.Components["external-postgres"] = true
	if mem, _ := c.Estimate(); mem != base+1536+128+16-256 {
		t.Errorf("external Postgres still counted: %d MB", mem)
	}
}
