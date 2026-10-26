package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"interzone/internal/catalog"
	"interzone/internal/config"
)

// press sends keys and runs what they return, as the program would.
func press(a *App, keys ...tea.KeyMsg) {
	for _, k := range keys {
		deliver(a, k)
	}
}

func deliver(a *App, msg tea.Msg) {
	_, cmd := a.Update(msg)
	for _, m := range flatten(cmd) {
		deliver(a, m)
	}
}

// flatten runs cmd and the commands it batches, skipping ticks and the like
// that would wait.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, flatten(c)...)
		}
		return out
	case noteMsg, navMsg, formDoneMsg, gotoMsg:
		return []tea.Msg{m}
	}
	return nil
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var enterMsg = tea.KeyMsg{Type: tea.KeyEnter}

// Saving a preset stays on the step, even from the form's last field.
func TestSavePreset(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Components"))
	press(a, runes("p"), tea.KeyMsg{Type: tea.KeyCtrlU}, runes("Mine"), enterMsg, runes("for us"), enterMsg)
	if a.cur().name() != "Components" {
		t.Fatalf("moved on to %s", a.cur().name())
	}
	p, ok := config.PresetNamed("mine")
	if !ok || p.Description != "for us" || !p.Matches(a.cfg) || a.cfg.Preset != p.ID {
		t.Fatalf("saved %+v, %v; config preset %q", p, ok, a.cfg.Preset)
	}
}

// A preset over components picked by hand wants enter twice.
func TestApplyPreset(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Homeserver"))
	hs := a.cur().(*homeserverScreen)
	hs.sel = len(hs.presets) - 1 // Lightweight
	press(a, enterMsg)
	if a.cur().name() != "Homeserver" || a.cfg.Homeserver != "synapse" {
		t.Fatal("replaced the components without asking")
	}
	press(a, enterMsg)
	if a.cur().name() != "Components" || a.cfg.Homeserver != "tuwunel" || !a.cfg.Components["cinny"] || a.cfg.Components["element"] {
		t.Fatalf("on %s with %s %v", a.cur().name(), a.cfg.Homeserver, a.cfg.Components)
	}
	if a.cfg.Preset != catalog.Presets[len(catalog.Presets)-1].ID {
		t.Errorf("preset %q", a.cfg.Preset)
	}
}

// Sable is pointed out among the clients.
func TestSableSuggested(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Components"))
	cs := a.cur().(*componentsScreen)
	for _, c := range catalog.Components {
		if c.ID == "sable" {
			if row := ansi.Strip(cs.row(c, 0, false, 100)); !strings.Contains(row, "suggested") {
				t.Errorf("no suggestion on Sable's row: %q", row)
			}
		}
	}
}

// Manage opens and closes MAS sign-up, and shows the token while it's open.
func TestManageSignup(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.cfg.Components["mas"] = true
	a.jump(a.index("Manage"))
	ms := a.cur().(*manageScreen)
	find := func() manageAction {
		for _, act := range ms.actions() {
			if act.id == "registration" {
				return act
			}
		}
		t.Fatal("no registration action")
		return manageAction{}
	}
	if find().title != "Open registration" {
		t.Fatalf("got %q", find().title)
	}
	a.cfg.Signup = true
	a.cfg.FillGenerated()
	if act := find(); act.title != "Close registration" || !strings.Contains(act.desc, a.cfg.SignupToken) {
		t.Errorf("got %q: %q", act.title, act.desc)
	}
}
