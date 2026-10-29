package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
)

// ── Go to ──────────────────────────────────────────────────────────
//
// ctrl+k, from anywhere: type a few letters of a screen or a Manage
// action, and enter goes there. It goes the way the menu does (sideClick),
// so a running deploy and a server not installed yet stop it the same way.

type gotoEntry struct {
	label string
	id    string // a menu id
	act   string // or a Manage action
}

func (a *App) openGoto() {
	in := textinput.New()
	in.Prompt = sAccent.Render("› ")
	in.Placeholder = "a screen or an action, e.g. backup"
	in.PlaceholderStyle = lipgloss.NewStyle().Foreground(cFaint)
	in.TextStyle = lipgloss.NewStyle().Foreground(cText)
	in.Focus()
	a.gotoIn, a.gotoSel, a.gotoOpen = in, 0, true
	a.menuOpen, a.sheetOpen, a.sideFocus = false, false, false
}

func (a *App) gotoEntries() []gotoEntry {
	var es []gotoEntry
	for i, s := range a.wizard() {
		if s.name() != "Deploy" {
			es = append(es, gotoEntry{label: "Setup › " + s.name(), id: "side:step:" + strconv.Itoa(i+1)})
		}
	}
	for _, m := range manageMenu {
		if (m.screen == "Users" || m.screen == "Rooms") && a.cfg.Homeserver != "synapse" || m.screen == "Tokens" && !a.cfg.UsesMAS() {
			continue
		}
		es = append(es, gotoEntry{label: "Manage › " + m.label, id: "side:manage:" + m.screen})
	}
	if a.cfg.Installed() {
		ms := a.screens[a.index("Manage")].(*manageScreen)
		ms.cfg = a.cfg
		for _, act := range ms.actions() {
			if act.disabled == "" && actionScreens[act.id] == "" { // those are listed above
				es = append(es, gotoEntry{label: "Manage › " + act.title, act: act.id})
			}
		}
	}
	for i, t := range mirrorsTabs {
		es = append(es, gotoEntry{label: "Mirrors › " + t, id: "side:mirrors:" + strconv.Itoa(i)})
	}
	es = append(es, gotoEntry{label: "Test servers", id: "side:test:"})
	for _, p := range config.Profiles() {
		if p.Name != config.Active() {
			es = append(es, gotoEntry{label: "Servers › switch to " + p.Name, id: "side:server:" + p.Name})
		}
	}
	return es
}

// gotoMatches are the entries holding every word typed, in any order.
func (a *App) gotoMatches() []gotoEntry {
	words := strings.Fields(strings.ToLower(a.gotoIn.Value()))
	var out []gotoEntry
next:
	for _, e := range a.gotoEntries() {
		for _, w := range words {
			if !strings.Contains(strings.ToLower(e.label), w) {
				continue next
			}
		}
		out = append(out, e)
	}
	return out
}

func (a *App) gotoKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	ms := a.gotoMatches()
	switch m.String() {
	case "esc", "ctrl+k", "ctrl+c":
		a.gotoOpen = false
		return a, nil
	case "up", "ctrl+p", "shift+tab":
		a.gotoSel = max(0, a.gotoSel-1)
		return a, nil
	case "down", "ctrl+n", "tab":
		a.gotoSel = min(len(ms)-1, a.gotoSel+1)
		return a, nil
	case "enter":
		if len(ms) == 0 {
			return a, nil
		}
		a.gotoOpen = false
		e := ms[min(a.gotoSel, len(ms)-1)]
		if e.act == "" {
			return a, a.sideClick(e.id)
		}
		cmd := a.sideClick("side:manage:Manage")
		mg, ok := a.cur().(*manageScreen)
		if !ok {
			return a, cmd // busy, or not installed: sideClick said why
		}
		for i, act := range mg.actions() {
			if act.id != e.act {
				continue
			}
			mg.cursor = i
			if runsAtOnce[act.id] { // changes the server: a second enter runs it
				mg.armed, mg.armedKey = true, "enter"
				return a, cmd
			}
			return a, tea.Batch(cmd, mg.choose(act.id))
		}
		return a, cmd
	}
	var cmd tea.Cmd
	a.gotoIn, cmd = a.gotoIn.Update(m)
	a.gotoSel = 0
	return a, cmd
}

func (a *App) gotoView(w, h int) string {
	a.gotoIn.Width = max(10, min(w, maxFormWidth)-4)
	lines := []string{a.gotoIn.View(), ""}
	ms := a.gotoMatches()
	if len(ms) == 0 {
		lines = append(lines, sMuted.Render("  nothing called that"))
	}
	room := max(1, h-len(lines))
	top := max(0, min(a.gotoSel-room+1, len(ms)-room))
	for i := top; i < min(len(ms), top+room); i++ {
		row := "  " + sTitle.Render(truncate(ms[i].label, w-2))
		if i == a.gotoSel {
			row = sAccent.Render("▌ ") + sFocus.Render(truncate(ms[i].label, w-2))
		}
		lines = append(lines, markRow("goto:"+strconv.Itoa(i), row, w))
	}
	return strings.Join(lines, "\n")
}
