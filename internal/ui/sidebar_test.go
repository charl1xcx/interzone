package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"interzone/internal/config"
)

// Opening one section of the menu leaves the others as they were.
func TestSectionsFoldAlone(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.sideClick("side:head:manage")
	a.sideClick("side:head:setup")
	if !a.sideOpen[sideManage] || !a.sideOpen[sideSetup] {
		t.Fatalf("opening Setup closed Manage: %v", a.sideOpen)
	}
	a.sideClick("side:head:setup")
	if a.sideOpen[sideSetup] || !a.sideOpen[sideManage] {
		t.Fatalf("closing Setup should leave Manage open: %v", a.sideOpen)
	}
	view := ansi.Strip(a.View())
	if !strings.Contains(view, "Overview") || strings.Contains(view, "Homeserver") {
		t.Errorf("menu doesn't match the folds:\n%s", view)
	}
	if got := loadSideOpen(); !got[sideManage] || got[sideSetup] {
		t.Errorf("folds weren't kept: %v", got)
	}

	// Going to a screen opens its section and nothing else changes.
	a.jump(a.index("Mirrors"))
	if !a.sideOpen[sideMirrors] || !a.sideOpen[sideManage] || a.sideOpen[sideSetup] {
		t.Errorf("after going to Mirrors: %v", a.sideOpen)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.cur().name() != "Welcome" {
		t.Errorf("esc from Mirrors led to %s", a.cur().name())
	}
}

func TestThemeCycles(t *testing.T) {
	demoApp(t, 120, 36)
	defer applyTheme(themes[0])
	for range themes {
		before := themeName
		cycleTheme()
		if themeName == before {
			t.Fatalf("ctrl+y stayed on %s", before)
		}
	}
	if themeName != themes[0].name {
		t.Errorf("a full cycle ends on %s", themeName)
	}
	if _, err := os.Stat(themeFile()); err == nil {
		t.Error("the default theme should leave no file behind")
	}
	cycleTheme()
	b, _ := os.ReadFile(themeFile())
	applyTheme(themes[0])
	loadTheme()
	if themeName != strings.TrimSpace(string(b)) {
		t.Errorf("saved %q, loaded %s", b, themeName)
	}
}

// Synapse comes with MAS on a new server, and MAS can be unticked.
func TestMASByDefault(t *testing.T) {
	if !config.Fresh().Components["mas"] {
		t.Fatal("a fresh setup should have MAS")
	}
	a := demoApp(t, 120, 36)
	a.cfg.Homeserver = "tuwunel"
	delete(a.cfg.Components, "mas")
	hs := a.screens[a.index("Homeserver")].(*homeserverScreen)
	a.jump(a.index("Homeserver"))
	hs.hs = 0 // Synapse
	hs.leave(a.cfg)
	if !a.cfg.Components["mas"] {
		t.Error("switching to Synapse should tick MAS")
	}
	a.cfg.Components["mas"] = false // unticked on Components
	hs.leave(a.cfg)
	if a.cfg.Components["mas"] {
		t.Error("staying on Synapse brought MAS back after it was unticked")
	}
}
