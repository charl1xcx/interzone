package catalog

import "testing"

// Every preset names a real homeserver and components it can run.
func TestPresets(t *testing.T) {
	comps := map[string]Component{}
	for _, c := range Components {
		comps[c.ID] = c
	}
	hs := map[string]bool{}
	for _, h := range Homeservers {
		hs[h.ID] = true
	}
	for _, p := range Presets {
		if !hs[p.Homeserver] {
			t.Errorf("%s: no homeserver %q", p.ID, p.Homeserver)
		}
		for _, id := range p.Components {
			c, ok := comps[id]
			switch {
			case !ok:
				t.Errorf("%s: no component %q", p.ID, id)
			case c.SynapseOnly && p.Homeserver != "synapse":
				t.Errorf("%s: %s needs Synapse", p.ID, id)
			case c.Status != "":
				t.Errorf("%s: %s is %s", p.ID, id, c.Status)
			}
		}
	}
}
