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
	a.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if a.View(); a.menuTop == 0 || a.menuSel != "app:theme" {
		t.Errorf("the menu didn't scroll to the selection (top %d, selected %q)", a.menuTop, a.menuSel)
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

// The menu works from the keyboard: ctrl+b, the arrows, enter.
func TestMenuKeys(t *testing.T) {
	key := func(a *App, k tea.KeyType) { a.Update(tea.KeyMsg{Type: k}) }

	a := demoApp(t, 120, 36) // wide: the keys go to the sidebar
	a.jump(a.index("Manage"))
	key(a, tea.KeyCtrlB)
	if !a.sideFocus || a.menuSel != "side:manage:Manage" {
		t.Fatalf("ctrl+b: focus %v, selected %q", a.sideFocus, a.menuSel)
	}
	if !strings.Contains(ansi.Strip(a.View()), "esc back to Manage") {
		t.Error("the footer doesn't show the menu's keys")
	}
	key(a, tea.KeyDown)
	if a.menuSel != "side:manage:Users" {
		t.Errorf("↓ selected %q", a.menuSel)
	}
	key(a, tea.KeyLeft) // to the heading, then fold it
	key(a, tea.KeyLeft)
	if a.menuSel != "side:head:manage" || a.sideOpen[sideManage] {
		t.Errorf("←←: selected %q, Manage open %v", a.menuSel, a.sideOpen[sideManage])
	}
	key(a, tea.KeyRight)
	if !a.sideOpen[sideManage] {
		t.Error("→ didn't unfold Manage")
	}
	a.menuSel = "side:mirrors:1"
	a.sideOpen[sideMirrors] = true
	key(a, tea.KeyEnter)
	if a.sideFocus || a.cur().name() != "Mirrors" {
		t.Errorf("enter led to %s (focus %v)", a.cur().name(), a.sideFocus)
	}
	key(a, tea.KeyCtrlB)
	key(a, tea.KeyEsc)
	if a.sideFocus || a.cur().name() != "Mirrors" {
		t.Errorf("esc should only leave the menu (on %s, focus %v)", a.cur().name(), a.sideFocus)
	}

	a = demoApp(t, 46, 40) // compact: the tiles, two across
	a.jump(a.index("Manage"))
	key(a, tea.KeyCtrlB)
	a.View()
	if !a.menuOpen || a.menuSel != "side:manage:Manage" {
		t.Fatalf("ctrl+b: open %v, selected %q", a.menuOpen, a.menuSel)
	}
	key(a, tea.KeyRight)
	if a.menuSel != "side:manage:Users" {
		t.Errorf("→ selected %q, want the tile beside Overview", a.menuSel)
	}
	key(a, tea.KeyDown)
	if a.menuSel != "side:manage:Updates" {
		t.Errorf("↓ selected %q, want the tile under Users", a.menuSel)
	}
	if v := ansi.Strip(a.View()); !strings.Contains(v, "›  Updates") {
		t.Errorf("the selected tile isn't marked:\n%s", v)
	}
}
