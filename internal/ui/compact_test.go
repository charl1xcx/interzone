package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"interzone/internal/catalog"
)

// The bottom bar's buttons are the keys they stand for.
func TestCompactBar(t *testing.T) {
	a := demoApp(t, 48, 20)
	a.jump(a.index("Server"))
	bar := ansi.Strip(a.compactBar(48))
	for _, want := range []string{"‹ back", "≡ menu", "next ›", "⋯"} {
		if !strings.Contains(bar, want) {
			t.Errorf("bar has no %q: %q", want, bar)
		}
	}
	a.tap([]string{"key:esc"})
	if a.cur().name() != "Welcome" {
		t.Errorf("‹ back led to %s", a.cur().name())
	}
	a.tap([]string{"app:menu"})
	if !a.menuOpen {
		t.Fatal("≡ menu didn't open the menu")
	}
	if bar := ansi.Strip(a.compactBar(48)); !strings.Contains(bar, "✕ close") {
		t.Errorf("an open menu's bar should only close it: %q", bar)
	}
	a.tap([]string{"key:esc"})
	if a.menuOpen {
		t.Error("✕ close left the menu open")
	}
}

// A row of the ⋯ sheet presses its key and puts the sheet away.
func TestSheetRunsKey(t *testing.T) {
	a := demoApp(t, 48, 20)
	a.jump(a.index("Manage"))
	a.tap([]string{"app:sheet"})
	if !a.sheetOpen {
		t.Fatal("⋯ didn't open the sheet")
	}
	view := ansi.Strip(a.View())
	if !strings.Contains(view, "ctrl+l") || strings.Contains(view, "Update the server") {
		t.Errorf("the sheet should cover the screen and list the keys:\n%s", view)
	}
	before := a.layoutName()
	a.tap([]string{"key:ctrl+l"})
	if a.sheetOpen {
		t.Error("the sheet stayed open")
	}
	if a.layoutName() == before {
		t.Errorf("tapping ctrl+l left the layout on %s", before)
	}
}

// With the keyboard up, a form shows the field being typed in and a line
// for each of the others.
func TestTightForm(t *testing.T) {
	a := demoApp(t, 48, 20)
	a.jump(a.index("Server"))
	view := ansi.Strip(a.View())
	if !strings.Contains(view, "› example.org") {
		t.Errorf("the focused field isn't shown in full:\n%s", view)
	}
	if !strings.Contains(view, "Install where?  This machine") {
		t.Errorf("the other fields should be one line each:\n%s", view)
	}
	if n := len(strings.Split(view, "\n")); n > 20 {
		t.Errorf("%d lines on a 20-line screen", n)
	}
}

// The compact menu's tiles go where the sidebar's entries do, and the
// menu scrolls when it's taller than the screen.
func TestMenuTiles(t *testing.T) {
	a := demoApp(t, 48, 20)
	a.jump(a.index("Manage"))
	a.tap([]string{"app:menu"})
	view := ansi.Strip(a.View())
	if !strings.Contains(view, "▦  Dashboard") || !strings.Contains(view, "▼ more below") {
		t.Fatalf("expected Manage's tiles and a scroll hint:\n%s", view)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if a.View(); a.menuTop == 0 {
		t.Error("↓ didn't scroll the menu")
	}
	a.tap([]string{"side:mirrors:1"})
	if a.menuOpen || a.cur().name() != "Mirrors" {
		t.Errorf("the Mirrors tile led to %s (menu open: %v)", a.cur().name(), a.menuOpen)
	}
}

// A preset's components are grouped by category, in the catalog's order.
func TestPresetGroups(t *testing.T) {
	var p catalog.Preset
	for _, x := range catalog.Presets {
		if x.ID == "family" {
			p = x
		}
	}
	n := 0
	for _, g := range presetGroups(p) {
		n += len(g.items)
		if g.label == "" {
			t.Errorf("a group without a label: %v", g.items)
		}
	}
	if n != len(p.Components) {
		t.Errorf("%d of %d components grouped", n, len(p.Components))
	}
}
