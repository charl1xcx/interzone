package config

import (
	"slices"
	"strings"
	"testing"

	"interzone/internal/catalog"
)

func TestPresets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := Default()
	c.ApplyPreset(catalog.Presets[0])
	if c.Homeserver != "synapse" || !c.Components["element"] || c.Components["cinny"] || c.Preset != "minimal" {
		t.Fatalf("applied: %s %v %q", c.Homeserver, c.Components, c.Preset)
	}
	ps, _ := Presets()
	if !ps[0].Matches(c) || ps[1].Matches(c) {
		t.Error("matches")
	}

	// Saving under a built-in name edits it; deleting brings it back.
	c.Components["cinny"] = true
	s := c.Stack()
	s.Name, s.Description = "minimal CHAT", "mine"
	if _, err := SavePreset(s); err != nil {
		t.Fatal(err)
	}
	ps, _ = Presets()
	if ps[0].ID != "minimal" || !ps[0].Edited || !slices.Contains(ps[0].Components, "cinny") || len(ps) != len(catalog.Presets) {
		t.Errorf("edited: %+v", ps[0])
	}
	if err := DeletePreset("minimal"); err != nil {
		t.Fatal(err)
	}
	if ps, _ = Presets(); ps[0].Edited || slices.Contains(ps[0].Components, "cinny") {
		t.Errorf("reset: %+v", ps[0])
	}

	// A new name makes a new preset, after the built-in ones.
	s.Name = "Light weight"
	if _, err := SavePreset(s); err != nil {
		t.Fatal(err)
	}
	ps, _ = Presets()
	last := ps[len(ps)-1]
	if last.Builtin || last.ID != "light-weight" || last.Name != "Light weight" {
		t.Errorf("new: %+v", last)
	}
	if p, ok := PresetNamed("LIGHT WEIGHT "); !ok || p.ID != "light-weight" {
		t.Errorf("by name: %+v", p)
	}

	// Synapse-only components are left out on other homeservers.
	c.ApplyPreset(catalog.Preset{Homeserver: "tuwunel", Components: []string{"ketesa", "cinny"}})
	if c.Components["ketesa"] || !c.Components["cinny"] {
		t.Errorf("tuwunel: %v", c.Components)
	}
}

// No preset turns on scheduled backups; they're off until asked for.
func TestPresetsLeaveBackupsOff(t *testing.T) {
	for _, p := range catalog.Presets {
		c := Default()
		c.ApplyPreset(p)
		if c.Backups.Enabled {
			t.Errorf("%s turned the backups on", p.ID)
		}
	}
}

// A new setup starts without the email relay, and since the playbook turns
// it on by itself, it's written out as off. Every preset has Sable and no
// relay.
func TestEximStartsOff(t *testing.T) {
	c := Fresh()
	c.Domain = "example.org"
	if c.Components["exim"] {
		t.Fatal("a new setup has exim ticked")
	}
	if y := c.VarsYAML("", "", ""); !strings.Contains(y, "exim_relay_enabled: false") {
		t.Errorf("exim isn't written as off:\n%s", y)
	}
	for _, p := range catalog.Presets {
		if !slices.Contains(p.Components, "sable") || slices.Contains(p.Components, "exim") {
			t.Errorf("%s: %v", p.ID, p.Components)
		}
	}
}
