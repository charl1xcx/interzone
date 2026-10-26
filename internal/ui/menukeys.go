package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── The menu from the keyboard ─────────────────────────────────────
//
// ctrl+b takes the keys to the menu: the sidebar in the wide layout, the
// tiles in the compact one. ↑↓ move between rows, ←→ along a row of tiles
// or fold a section, enter opens what's selected and esc goes back to
// the screen. The selection is kept by id, so it stays put while sections
// fold and unfold around it.

// menuEntry is a place the selection can be: a heading, an entry, or one
// of the links under them, at a row and column of the menu as drawn.
type menuEntry struct {
	id       string
	row, col int
	sec      sideSection
	head     bool
}

// menuEntries lists them in reading order.
func (a *App) menuEntries() []menuEntry {
	cols := 1
	if a.menuOpen {
		cols = max(1, a.menuCols) // 0 until the menu is first drawn
	}
	var es []menuEntry
	row := 0
	for _, m := range a.menu() {
		es = append(es, menuEntry{id: "side:head:" + sectionNames[m.sec], row: row, sec: m.sec, head: true})
		row++
		for i, it := range m.items {
			es = append(es, menuEntry{id: it.id, row: row + i/cols, col: i % cols, sec: m.sec})
		}
		row += (len(m.items) + cols - 1) / cols
	}
	if a.menuOpen { // the compact menu has its two links on one row
		return append(es, menuEntry{id: "app:layout", row: row}, menuEntry{id: "app:theme", row: row, col: 1})
	}
	return append(es, menuEntry{id: "app:layout", row: row}, menuEntry{id: "app:theme", row: row + 1},
		menuEntry{id: "side:fold", row: row + 2})
}

// inMenu reports whether the keys go to the menu.
func (a *App) inMenu() bool { return a.menuOpen || a.sideFocus }

// focused reports whether id is selected and the selection is shown: it
// is once the keys are on the menu, not while it's being tapped.
func (a *App) focused(id string) bool {
	return a.inMenu() && a.menuKeys && a.menuSel == id
}

// selectHere puts the selection where you are: the current entry, or its
// section's heading.
func (a *App) selectHere() {
	a.menuSel = ""
	for _, m := range a.menu() {
		if m.sec == a.section() && a.menuSel == "" {
			a.menuSel = "side:head:" + sectionNames[m.sec]
		}
		for _, it := range m.items {
			if it.on {
				a.menuSel = it.id
			}
		}
	}
	if a.menuSel == "" {
		a.menuSel = "side:head:setup"
	}
}

// focusSidebar takes the keys to the wide layout's sidebar.
func (a *App) focusSidebar() {
	if a.sidebarRail() {
		if !a.sideCollapsed {
			a.status = sWarn.Render("! the window is too narrow for the menu; widen it, or ctrl+l for the compact layout")
			return
		}
		a.toggleSidebar()
	}
	a.sideFocus, a.menuKeys = true, true
	a.selectHere()
}

func (a *App) leaveMenu() {
	a.menuOpen, a.sideFocus = false, false
}

func (a *App) menuKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !a.menuKeys { // opened with a tap: start from where you are
		a.selectHere()
	}
	es := a.menuEntries()
	i := 0
	for j, e := range es {
		if e.id == a.menuSel {
			i = j
		}
	}
	cur := es[i]
	a.menuSel, a.menuKeys, a.menuFollow = cur.id, true, true
	// row finds the entry on row r nearest column col.
	row := func(r, col int) int {
		best := -1
		for j, e := range es {
			if e.row == r && (best < 0 || e.col <= col) {
				best = j
			}
		}
		return best
	}
	move := func(j int) {
		if j >= 0 && j < len(es) {
			a.menuSel = es[j].id
		}
	}
	switch m.String() {
	case "esc", "ctrl+b", "tab", "q":
		a.leaveMenu()
	case "ctrl+c":
		a.leaveMenu()
		return a.Update(m)
	case "up", "k":
		move(row(cur.row-1, cur.col))
	case "down", "j":
		move(row(cur.row+1, cur.col))
	case "left", "h":
		switch {
		case cur.col > 0:
			move(i - 1)
		case cur.head && a.sideOpen[cur.sec]:
			a.toggleSection(cur.sec)
		case !cur.head && cur.sec != sideNone:
			a.menuSel = "side:head:" + sectionNames[cur.sec]
		}
	case "right", "l":
		switch {
		case i+1 < len(es) && es[i+1].row == cur.row:
			move(i + 1)
		case cur.head && !a.sideOpen[cur.sec]:
			a.toggleSection(cur.sec)
		}
	case "home", "g":
		move(0)
	case "end", "G":
		move(len(es) - 1)
	case "enter", " ":
		return a.menuPick(cur)
	}
	return a, nil
}

// menuPick opens what's selected. A heading folds or unfolds its section
// and the links switch the layout or theme, all leaving the keys on the
// menu; anything else goes there and gives the keys to that screen.
func (a *App) menuPick(e menuEntry) (tea.Model, tea.Cmd) {
	switch {
	case e.head:
		a.toggleSection(e.sec)
		return a, nil
	case e.id == "app:layout":
		on := a.inMenu()
		a.cycleLayout()
		// The menu follows into the other layout.
		a.menuOpen, a.sideFocus = on && a.compact(), on && !a.compact()
		if a.sideFocus && a.sidebarRail() {
			a.sideFocus = false
		}
		return a, nil
	case e.id == "app:theme":
		cycleTheme()
		return a, nil
	case e.id == "side:fold":
		a.sideFocus = false
		a.toggleSidebar()
		return a, nil
	}
	a.leaveMenu()
	return a, a.sideClick(e.id)
}

// menuMarker is the first column of a selected line in the sidebar.
func menuMarker() string { return sAccent.Render("›") }

// sFocus is the selected entry's label.
var sFocus = lipgloss.NewStyle().Bold(true).Underline(true)

// menuKeyHints are the footer's keys while the sidebar has them.
func (a *App) menuKeyHints() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"←→", "fold"}, {"enter", "open"}, {"esc", "back to " + a.cur().name()}}
}
