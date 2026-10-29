package config

import (
	"strings"
	"testing"
)

func TestLocks(t *testing.T) {
	c := Fresh()
	c.Domain, c.Homeserver, c.Components["mas"] = "example.org", "synapse", false
	if c.Locked("domain") != "" || c.Breaks() != nil {
		t.Fatal("locked before it was deployed")
	}
	c.MarkDeployed()
	for _, what := range []string{"domain", "homeserver", "mas"} {
		if c.Locked(what) == "" {
			t.Errorf("%s isn't locked", what)
		}
	}
	if c.Locked("hookshot") != "" || c.Breaks() != nil {
		t.Errorf("hookshot locked, or breaks %v", c.Breaks())
	}
	c.Domain, c.Homeserver, c.Components["mas"] = "other.org", "tuwunel", true
	if b := c.Breaks(); len(b) != 2 || !strings.Contains(b[0], "other.org") {
		t.Errorf("breaks %v", b)
	}
	c.Homeserver = "synapse"
	if b := c.Breaks(); len(b) != 2 || b[1] != "MAS was turned on" {
		t.Errorf("breaks %v", b)
	}
	c.KeepDeployed()
	if c.Domain != "example.org" || c.Components["mas"] || c.Breaks() != nil {
		t.Errorf("not kept: %s %v", c.Domain, c.Components["mas"])
	}

	// MAS is only Synapse's to lock.
	c.Homeserver = "tuwunel"
	c.MarkDeployed()
	if c.Locked("mas") != "" {
		t.Error("MAS locked on Tuwunel")
	}
}
