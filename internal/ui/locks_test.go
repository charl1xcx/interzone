package ui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/catalog"
)

// Once the server runs, the setup can't change its domain, homeserver or
// MAS; everything else still changes.
func TestSetupLocks(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.cfg.Domain, a.cfg.Homeserver, a.cfg.Components["mas"] = "example.org", "synapse", true
	a.cfg.MarkDeployed()

	a.jump(a.index("Server"))
	ss := a.cur().(*serverScreen)
	ss.form.focusOn(0)
	if ss.form.fields[0].key != "domain" {
		t.Fatal("the domain isn't the first field")
	}
	ss.update(runes("x"))
	ss.leave(a.cfg)
	if a.cfg.Domain != "example.org" || !strings.Contains(ss.view(120, 34), "locked") {
		t.Errorf("domain changed to %q", a.cfg.Domain)
	}

	a.jump(a.index("Homeserver"))
	hs := a.cur().(*homeserverScreen)
	for i, h := range catalog.Homeservers {
		if h.ID == "tuwunel" {
			hs.sel = len(hs.presets) + i
		}
	}
	hs.update(tea.KeyMsg{Type: tea.KeyEnter})
	hs.leave(a.cfg)
	if a.cfg.Homeserver != "synapse" || !strings.Contains(hs.err, "can't switch homeservers") {
		t.Errorf("homeserver %s, err %q", a.cfg.Homeserver, hs.err)
	}
	// A Synapse preset with MAS leaves a server without it as it is.
	a.cfg.Deployed.MAS, a.cfg.Components["mas"] = false, false
	ran := false
	for i, p := range hs.presets {
		if p.Homeserver == "synapse" && slices.Contains(p.Components, "mas") {
			hs.sel = i
			hs.choose()
			hs.choose() // the second enter confirms replacing the components
			ran = true
			break
		}
	}
	if !ran || a.cfg.Components["mas"] || a.cfg.Preset == "" {
		t.Errorf("preset applied %v (%q), MAS %v", ran, a.cfg.Preset, a.cfg.Components["mas"])
	}
	a.cfg.Deployed.MAS, a.cfg.Components["mas"] = true, true

	a.jump(a.index("Components"))
	cs := a.cur().(*componentsScreen)
	mas, _ := catalog.ByID("mas")
	if cs.toggles(mas) {
		t.Error("MAS can be unticked")
	}
	hook, _ := catalog.ByID("hookshot")
	if !cs.toggles(hook) {
		t.Error("hookshot is locked too")
	}
}
