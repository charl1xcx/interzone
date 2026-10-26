package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
)

// ── Sidebar ────────────────────────────────────────────────────────
//
// A menu on the left showing where you are: the setup wizard's steps, the
// Manage screens, the mirror checks and the saved servers. Each section
// folds on its own: clicking a heading opens or closes just that one, and
// going to a screen opens its section without closing the others. ctrl+b
// folds the whole menu to a narrow rail of icons; narrow terminals always
// get the rail.

const (
	sidebarW     = 26
	sidebarRailW = 5
	sidebarMinW  = 100 // below this terminal width only the rail fits
)

type sideSection int

const (
	sideNone sideSection = iota
	sideSetup
	sideManage
	sideMirrors
	sideServers
)

var sectionNames = map[sideSection]string{sideSetup: "setup", sideManage: "manage", sideMirrors: "mirrors", sideServers: "servers"}

func sideOpenFile() string { return filepath.Join(config.Dir(), "sidebar-open") }

// loadSideOpen reads which sections were left open. Before anything was
// saved only Servers is.
func loadSideOpen() map[sideSection]bool {
	b, err := os.ReadFile(sideOpenFile())
	if err != nil {
		return map[sideSection]bool{sideServers: true}
	}
	open := map[sideSection]bool{}
	for _, name := range strings.Fields(string(b)) {
		for sec, n := range sectionNames {
			if n == name {
				open[sec] = true
			}
		}
	}
	return open
}

func (a *App) saveSideOpen() {
	var names []string
	for _, sec := range []sideSection{sideSetup, sideManage, sideMirrors, sideServers} {
		if a.sideOpen[sec] {
			names = append(names, sectionNames[sec])
		}
	}
	_ = os.MkdirAll(config.Dir(), 0o700)
	_ = os.WriteFile(sideOpenFile(), []byte(strings.Join(names, "\n")+"\n"), 0o600)
}

// openSection unfolds sec, leaving the others as they are.
func (a *App) openSection(sec sideSection) {
	if sec == sideNone || a.sideOpen[sec] {
		return
	}
	a.sideOpen[sec] = true
	a.saveSideOpen()
}

// toggleSection folds or unfolds sec alone.
func (a *App) toggleSection(sec sideSection) {
	a.sideOpen[sec] = !a.sideOpen[sec]
	a.saveSideOpen()
}

// manageMenu is the Manage section in the order Manage lists its tools.
var manageMenu = []struct{ screen, label, icon string }{
	{"Manage", "Overview", "⌂"},
	{"Users", "Users", "@"},
	{"Dashboard", "Dashboard", "▦"},
	{"Updates", "Updates", "↻"},
	{"Backups", "Backups", "◷"},
	{"Restore", "Restore", "↺"},
	{"Rollback", "Rollback", "↶"},
	{"Tuning", "Fine-tune", "✎"},
	{"Move", "Move server", "⇢"},
	{"Servers", "Servers", "▤"},
}

func sidebarStateFile() string { return filepath.Join(config.Dir(), "sidebar-collapsed") }

func loadSidebarCollapsed() bool {
	_, err := os.Stat(sidebarStateFile())
	return err == nil
}

func (a *App) toggleSidebar() {
	a.sideCollapsed = !a.sideCollapsed
	if a.sideCollapsed {
		_ = os.MkdirAll(config.Dir(), 0o700)
		_ = os.WriteFile(sidebarStateFile(), nil, 0o600)
	} else {
		_ = os.Remove(sidebarStateFile())
	}
}

// sidebarRail reports whether only the icon rail is shown.
func (a *App) sidebarRail() bool { return a.sideCollapsed || a.w < sidebarMinW }

func (a *App) sidebarWidth() int {
	if a.sidebarRail() {
		return sidebarRailW
	}
	return sidebarW
}

// section is the menu section the current screen belongs to.
func (a *App) section() sideSection {
	name := a.cur().name()
	switch {
	case a.step == 0:
		return sideNone
	case name == "Servers":
		return sideServers
	case name == "Mirrors":
		return sideMirrors
	case name == "Manage", a.isTool(a.step):
		return sideManage
	case name == "Deploy":
		if b := a.deployBack; a.screens[b].name() == "Manage" || a.isTool(b) {
			return sideManage
		}
	}
	return sideSetup
}

func (a *App) isTool(i int) bool { _, ok := a.screens[i].(toolScreen); return ok }

// manageCurrent is the Manage entry to highlight: the screen itself, or
// the one a running action was started from.
func (a *App) manageCurrent() string {
	if a.cur().name() == "Deploy" {
		return a.screens[a.deployBack].name()
	}
	return a.cur().name()
}

func (a *App) sidebar(h int) string {
	if a.sidebarRail() {
		sec := a.section()
		icon := func(s sideSection, glyph string) string {
			if s == sec {
				return sAccent.Render("▌" + glyph)
			}
			return sFaint.Render(" " + glyph)
		}
		lines := []string{mark("side:head:setup", icon(sideSetup, "◇")), "", mark("side:head:manage", icon(sideManage, "◆")), "",
			mark("side:head:mirrors", icon(sideMirrors, "⇅")), "", mark("side:head:servers", icon(sideServers, "▤"))}
		if !a.sideCollapsed {
			lines = append(lines, "", "", sFaint.Render(" ⋯")) // too narrow; ctrl+b does nothing here
		} else {
			lines = append(lines, "", "", mark("side:fold", sFaint.Render(" ›")))
		}
		return sPanel.Padding(0).Width(sidebarRailW - 2).Height(h).Render(strings.Join(lines, "\n"))
	}

	lines := a.menuLines(sidebarW - 4)
	// The layout switch and the fold hint sit at the bottom.
	body := strings.Join(lines, "\n")
	if gap := h - lipgloss.Height(body) - 3; gap > 0 {
		body += strings.Repeat("\n", gap)
	}
	body += "\n" + a.layoutLink() + "\n" + themeLink() + "\n" + mark("side:fold", sFaint.Render("◂ fold  ctrl+b"))
	return sPanel.Padding(0, 1).Width(sidebarW - 2).Height(h).Render(body)
}

// menuItem is one entry of the menu: its zone id, an icon already
// coloured, its label, a note under it, and whether it's where you are.
type menuItem struct {
	id, icon, label, sub string
	on                   bool
}

// menuSection is a heading of the menu and, when it's open, its entries.
type menuSection struct {
	sec         sideSection
	icon, title string
	items       []menuItem
}

// menu is what the menu shows, for the sidebar's list and the compact
// layout's tiles alike.
func (a *App) menu() []menuSection {
	sec := a.section()
	setup := menuSection{sec: sideSetup, icon: "◇", title: "Setup"}
	if a.sideOpen[sideSetup] {
		for i, s := range a.wizard() {
			idx := i + 1
			it := menuItem{id: "side:step:" + strconv.Itoa(idx), icon: sFaint.Render("○"), label: s.name()}
			switch {
			case idx == a.step && sec == sideSetup:
				it.icon, it.on = sAccent.Render("◉"), true
			case idx < a.step && sec == sideSetup, sec != sideSetup && a.cfg.Installed():
				it.icon = sGood.Render("●")
			}
			setup.items = append(setup.items, it)
		}
	}

	title := "Manage"
	if a.cfg.Domain != "" {
		title = "Manage " + a.cfg.Domain
	}
	manage := menuSection{sec: sideManage, icon: "◆", title: title}
	if a.sideOpen[sideManage] {
		cur := ""
		if sec == sideManage {
			cur = a.manageCurrent()
		}
		for _, m := range manageMenu {
			if m.screen == "Users" && a.cfg.Homeserver != "synapse" {
				continue
			}
			it := menuItem{id: "side:manage:" + m.screen, icon: sMuted.Render(m.icon), label: m.label, on: m.screen == cur}
			if it.on {
				it.icon = sAccent.Render(m.icon)
				if a.cur().name() == "Deploy" {
					it.sub = a.deployLabel()
				}
			}
			manage.items = append(manage.items, it)
		}
	}

	mirrors := menuSection{sec: sideMirrors, icon: "⇅", title: "Mirrors"}
	if a.sideOpen[sideMirrors] {
		ms := a.screens[a.index("Mirrors")].(*mirrorsScreen)
		for i, t := range mirrorsTabs {
			it := menuItem{id: "side:mirrors:" + strconv.Itoa(i), icon: sFaint.Render("·"), label: t, on: sec == sideMirrors && ms.tab == i}
			if ok, n := ms.tally(i); n > 0 {
				it.icon = sGood.Render("✓")
				if ok < n {
					it.icon = sBad.Render("✗")
				}
			}
			mirrors.items = append(mirrors.items, it)
		}
	}

	servers := menuSection{sec: sideServers, icon: "▤", title: "Servers"}
	if a.sideOpen[sideServers] {
		profiles := config.Profiles()
		if len(profiles) == 0 {
			profiles = []config.Profile{{Name: config.DefaultProfile, Domain: a.cfg.Domain}}
		}
		for _, p := range profiles {
			active := p.Name == config.Active()
			it := menuItem{id: "side:server:" + p.Name, icon: sFaint.Render("○"), label: p.Name, on: active && sec == sideServers}
			if active {
				it.icon = sGood.Render("●")
			}
			if p.Domain != "" && p.Name == config.DefaultProfile {
				it.label = p.Domain
			}
			servers.items = append(servers.items, it)
		}
	}
	return []menuSection{setup, manage, mirrors, servers}
}

// menuHead is a section's heading: an arrow showing whether it's open.
func (a *App) menuHead(m menuSection, w int) string {
	arrow := "▸ "
	if a.sideOpen[m.sec] {
		arrow = "▾ "
	}
	title := truncate(m.title, w-2)
	if m.sec == a.section() {
		return sSection.Render(arrow + title)
	}
	return sFaint.Render(arrow) + sMuted.Render(title)
}

// menuLines are the sidebar's entries, inner columns wide.
func (a *App) menuLines(inner int) []string {
	var lines []string
	for i, m := range a.menu() {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, mark("side:head:"+sectionNames[m.sec], a.menuHead(m, inner)))
		for _, it := range m.items {
			label := truncate(it.label, inner-4)
			line := "  " + it.icon + " " + sMuted.Render(label)
			if it.on {
				line = sAccent.Render("▌") + " " + it.icon + " " + sTitle.Render(label)
			}
			lines = append(lines, markRow(it.id, line, inner))
			if it.sub != "" {
				lines = append(lines, "    "+sPink.Render("↳ ")+sSub.Render(truncate(it.sub, inner-6)))
			}
		}
	}
	return lines
}

// menuTiles is the compact layout's menu: each heading a full-width row
// to tap, and an open section's entries as tiles, two across when they
// fit.
func (a *App) menuTiles(inner int) string {
	cols := 1
	if inner >= 32 {
		cols = 2
	}
	var parts []string
	for _, m := range a.menu() {
		arrow := sFaint.Render("▸")
		if a.sideOpen[m.sec] {
			arrow = sFaint.Render("▾")
		}
		icon, title := sMuted.Render(m.icon), sMuted.Render(truncate(m.title, inner-6))
		if m.sec == a.section() {
			icon, title = sAccent.Render(m.icon), sSection.Render(truncate(m.title, inner-6))
		}
		head := icon + "  " + title
		head += strings.Repeat(" ", max(1, inner-lipgloss.Width(head)-1)) + arrow
		parts = append(parts, markRow("side:head:"+sectionNames[m.sec], head, inner))
		if len(m.items) == 0 {
			continue
		}
		var tiles []func(w, h int) string
		for _, it := range m.items {
			tiles = append(tiles, menuTile(it.id, it.icon, it.label, it.sub, it.on))
		}
		parts = append(parts, tileGrid(tiles, inner, cols), "")
	}
	return strings.Join(parts, "\n")
}

// deployLabel names what the Deploy screen is running, for the sidebar.
func (a *App) deployLabel() string {
	ds, ok := a.cur().(*deployScreen)
	if !ok {
		return ""
	}
	t := actionTitles[ds.opts.Action]
	switch {
	case ds.running:
		return strings.TrimSuffix(t[0], "…")
	case ds.err != nil:
		return t[2]
	}
	return t[1]
}
