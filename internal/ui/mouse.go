package ui

import (
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"interzone/internal/config"
)

// ── Mouse ──────────────────────────────────────────────────────────
//
// Clickable parts of the view are wrapped with mark(id, text). The final
// frame is scanned for the markers, and a click is resolved to the ids
// under the pointer, smallest zone first:
//
//   key:<name>       a key hint; clicking presses the key
//   side:…           the menu on the left (sidebar.go)
//   app:…            the compact layout's menu button, the layout switch
//   anything else    handled by the screen's click method, or its form
//
// The wheel scrolls by sending ↑ and ↓ to the screen.

// clicker is a screen with clickable parts of its own.
type clicker interface {
	// click handles a click on id and reports whether it did.
	click(id string) (tea.Cmd, bool)
}

// formHolder is a screen showing a form that should take clicks.
type formHolder interface{ activeForm() *form }

// marked lists the zone ids of the frame being drawn. A mark's zone id
// gets the occurrence number appended, so the same id can be marked more
// than once in a frame (a key in the footer and in a dialog).
var marked []string

func mark(id, s string) string {
	if s == "" {
		return s
	}
	zid := id + "#" + strconv.Itoa(len(marked))
	marked = append(marked, zid)
	return zone.Mark(zid, s)
}

// markLines marks every line of s separately with id, so a block that
// is later cut into lines (scrolling) still resolves.
func markLines(id, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = mark(id, l)
	}
	return strings.Join(lines, "\n")
}

// markRow marks every line of a list row padded to w columns, so a tap
// anywhere across the row hits it, not only on its text.
func markRow(id, s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if gap := w - lipgloss.Width(l); gap > 0 {
			l += strings.Repeat(" ", gap)
		}
		lines[i] = mark(id, l)
	}
	return strings.Join(lines, "\n")
}

// hits returns the ids under the pointer, smallest zone first.
func hits(m tea.MouseMsg) []string {
	type hit struct {
		id   string
		area int
	}
	var hs []hit
	for _, zid := range marked {
		z := zone.Get(zid)
		if !z.InBounds(m) {
			continue
		}
		id := zid[:strings.LastIndex(zid, "#")]
		hs = append(hs, hit{id, (z.EndX - z.StartX + 1) * (z.EndY - z.StartY + 1)})
	}
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].area < hs[j].area })
	var out []string
	seen := map[string]bool{}
	for _, h := range hs {
		if !seen[h.id] {
			seen[h.id] = true
			out = append(out, h.id)
		}
	}
	return out
}

// keyMsg turns a key name as the hints spell it into a key press, or
// reports false for names that aren't one key ("↑↓", "type").
func keyMsg(name string) (tea.KeyMsg, bool) {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"del": tea.KeyDelete, "delete": tea.KeyDelete, "backspace": tea.KeyBackspace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"↑": tea.KeyUp, "↓": tea.KeyDown, "←": tea.KeyLeft, "→": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	if t, ok := named[name]; ok {
		return tea.KeyMsg{Type: t}, true
	}
	if name == "space" {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, true
	}
	if c, ok := strings.CutPrefix(name, "ctrl+"); ok && len(c) == 1 && c[0] >= 'a' && c[0] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(c[0] - 'a' + 1)}, true
	}
	if r := []rune(name); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}, true
	}
	return tea.KeyMsg{}, false
}

// keyLink renders a key name as a clickable hint. Combined hints are
// split into their keys: "↑↓" → ↑ and ↓, "tab/↑↓" → tab, ↑ and ↓.
func keyLink(name string) string {
	if strings.Contains(name, "/") && len(name) > 1 {
		parts := strings.Split(name, "/")
		for i, p := range parts {
			parts[i] = keyLink(p)
		}
		return strings.Join(parts, sKey.Render("/"))
	}
	if r := []rune(name); len(r) == 2 && strings.ContainsRune("↑←", r[0]) {
		return keyLink(string(r[0])) + keyLink(string(r[1]))
	}
	if _, ok := keyMsg(name); !ok {
		return sKey.Render(name)
	}
	return mark("key:"+name, sKey.Render(name))
}

// rowClick handles a click on a list row id "<prefix><n>": it moves the
// cursor to the row and reports whether the row was already selected.
func rowClick(id, prefix string, cursor *int, n int) (row int, again, ok bool) {
	rest, found := strings.CutPrefix(id, prefix)
	if !found {
		return 0, false, false
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 0 || i >= n {
		return 0, false, false
	}
	again = *cursor == i
	*cursor = i
	return i, again, true
}

func (a *App) mouse(m tea.MouseMsg) (tea.Model, tea.Cmd) {
	ids := hits(m)
	onSide := false
	for _, id := range ids {
		onSide = onSide || strings.HasPrefix(id, "side:")
	}
	switch {
	case m.Action != tea.MouseActionPress:
		return a, nil
	case m.Button == tea.MouseButtonWheelUp || m.Button == tea.MouseButtonWheelDown:
		if a.menuOpen && a.compact() {
			if m.Button == tea.MouseButtonWheelUp {
				a.menuTop = max(0, a.menuTop-3)
			} else {
				a.menuTop += 3
			}
			return a, nil
		}
		if onSide || a.menuOpen || a.sheetOpen || a.cur().typing() && !a.wheelOK() {
			return a, nil
		}
		k := tea.KeyMsg{Type: tea.KeyDown}
		if m.Button == tea.MouseButtonWheelUp {
			k.Type = tea.KeyUp
		}
		return a, a.cur().update(k)
	case m.Button != tea.MouseButtonLeft:
		return a, nil
	}
	return a.tap(ids)
}

// tap acts on a click on ids, smallest first.
func (a *App) tap(ids []string) (tea.Model, tea.Cmd) {
	for _, id := range ids {
		switch id {
		case "app:menu":
			a.menuOpen, a.sheetOpen, a.menuTop = !a.menuOpen, false, 0
			return a, nil
		case "app:sheet":
			a.sheetOpen, a.menuOpen = !a.sheetOpen, false
			return a, nil
		case "app:layout":
			a.cycleLayout()
			return a, nil
		case "app:theme":
			cycleTheme()
			return a, nil
		}
		if (a.menuOpen || a.sheetOpen) && !strings.HasPrefix(id, "side:") && !strings.HasPrefix(id, "key:") {
			continue // the screen under the menu isn't shown
		}
		if name, ok := strings.CutPrefix(id, "key:"); ok {
			if k, ok := keyMsg(name); ok {
				return a.Update(k)
			}
		}
		if strings.HasPrefix(id, "side:") {
			return a, a.sideClick(id)
		}
		if c, ok := a.cur().(clicker); ok {
			if cmd, done := c.click(id); done {
				return a, cmd
			}
		}
		if fh, ok := a.cur().(formHolder); ok {
			if f := fh.activeForm(); f != nil {
				if cmd, done := f.click(id); done {
					return a, cmd
				}
			}
		}
	}
	return a, nil
}

// wheelOK reports whether ↑↓ only move around on the current screen even
// while it takes typing (a form's text field), so the wheel can send them.
func (a *App) wheelOK() bool {
	if fh, ok := a.cur().(formHolder); ok {
		if f := fh.activeForm(); f != nil {
			c := f.current()
			return c == nil || c.kind != fArea
		}
	}
	return false
}

// busy reports whether a deploy runs, during which the menu can't leave.
func (a *App) busy() bool {
	ds, ok := a.cur().(*deployScreen)
	return ok && ds.running
}

func (a *App) sideClick(id string) tea.Cmd {
	inMenu := a.menuOpen
	a.menuOpen = false
	if id == "side:fold" {
		a.toggleSidebar()
		return nil
	}
	if a.busy() {
		a.status = sWarn.Render("! wait for the run to finish, or press ctrl+c to stop it")
		return nil
	}
	rest := strings.TrimPrefix(id, "side:")
	kind, arg, _ := strings.Cut(rest, ":")
	switch kind {
	case "step":
		n, err := strconv.Atoi(arg)
		if err != nil || a.screens[n].name() == "Deploy" {
			return nil // a deploy starts from Review
		}
		return a.jump(n)
	case "manage":
		if !a.cfg.Installed() && arg != "Servers" {
			a.status = sWarn.Render("! set up and deploy the server first; Manage works on an installed server")
			return nil
		}
		return a.jump(a.index(arg))
	case "server":
		if arg == config.Active() {
			return a.jump(a.index("Servers"))
		}
		c, err := config.LoadFile(config.ProfilePath(arg))
		if err != nil {
			a.status = sBad.Render("✗ " + err.Error())
			return nil
		}
		return a.switchServer(switchServerMsg{name: arg, cfg: c})
	case "mirrors":
		n, _ := strconv.Atoi(arg)
		a.screens[a.index("Mirrors")].(*mirrorsScreen).show(n)
		return a.jump(a.index("Mirrors"))
	case "head":
		if inMenu || !a.compact() && !a.sidebarRail() {
			// The full menu: the heading folds or unfolds its section alone.
			for sec, name := range sectionNames {
				if name == arg {
					a.toggleSection(sec)
				}
			}
			a.menuOpen = inMenu
			return nil
		}
		switch arg {
		case "setup":
			if a.section() != sideSetup {
				return a.jump(a.index("Server"))
			}
		case "manage":
			return a.sideClick("side:manage:Manage")
		case "mirrors":
			return a.jump(a.index("Mirrors"))
		case "servers":
			return a.jump(a.index("Servers"))
		}
	}
	return nil
}
