package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
