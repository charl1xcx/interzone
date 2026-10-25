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
// Manage screens, and the saved servers. The section holding the current
// screen is open and the others are folded to their heading. ctrl+b folds
// the whole menu to a narrow rail of icons; narrow terminals always get
// the rail.

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
	sideServers
)

// manageMenu is the Manage section in the order Manage lists its tools.
var manageMenu = []struct{ screen, label string }{
	{"Manage", "Overview"},
	{"Users", "Users"},
	{"Dashboard", "Dashboard"},
	{"Updates", "Updates"},
	{"Backups", "Scheduled backups"},
	{"Restore", "Restore"},
	{"Rollback", "Rollback"},
	{"Tuning", "Fine-tune"},
	{"Move", "Move server"},
	{"Servers", "Servers"},
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
	sec := a.section()
	if a.sidebarRail() {
		icon := func(s sideSection, glyph string) string {
			if s == sec {
				return sAccent.Render("▌" + glyph)
			}
			return sFaint.Render(" " + glyph)
		}
		lines := []string{mark("side:head:setup", icon(sideSetup, "◇")), "", mark("side:head:manage", icon(sideManage, "◆")), "",
			mark("side:head:servers", icon(sideServers, "▤"))}
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
	if gap := h - lipgloss.Height(body) - 2; gap > 0 {
		body += strings.Repeat("\n", gap)
	}
	body += "\n" + a.layoutLink() + "\n" + mark("side:fold", sFaint.Render("ctrl+b ‹ fold"))
	return sPanel.Padding(0, 1).Width(sidebarW - 2).Height(h).Render(body)
}

// menuLines are the menu's entries, inner columns wide: the sidebar's, and
// the menu screen's in the compact layout.
func (a *App) menuLines(inner int) []string {
	sec := a.section()
	item := func(on bool, dot, label string) string {
		label = truncate(label, inner-4)
		if on {
			return sAccent.Render("▌") + " " + dot + " " + sTitle.Render(label)
		}
		return "  " + dot + " " + sMuted.Render(label)
	}
	head := func(s sideSection, title string, open bool) string {
		switch {
		case s == sec:
			return sSection.Render("▾ " + title)
		case open:
			return sMuted.Render("▾ " + title)
		}
		return sFaint.Render("▸ ") + sMuted.Render(title)
	}

	var lines []string
	// Setup wizard
	lines = append(lines, mark("side:head:setup", head(sideSetup, "Setup", false)))
	if sec == sideSetup {
		for i, s := range a.wizard() {
			idx := i + 1
			var line string
			switch {
			case idx == a.step:
				line = item(true, sAccent.Render("◉"), s.name())
			case idx < a.step:
				line = item(false, sGood.Render("●"), s.name())
			default:
				line = item(false, sFaint.Render("○"), s.name())
			}
			lines = append(lines, markRow("side:step:"+strconv.Itoa(idx), line, inner))
		}
	}
	lines = append(lines, "")

	// Manage
	title := "Manage"
	if a.cfg.Domain != "" {
		title = "Manage " + a.cfg.Domain
	}
	lines = append(lines, mark("side:head:manage", head(sideManage, truncate(title, inner-2), false)))
	if sec == sideManage {
		cur := a.manageCurrent()
		for _, m := range manageMenu {
			if m.screen == "Users" && a.cfg.Homeserver != "synapse" {
				continue
			}
			dot := sFaint.Render("·")
			if m.screen == cur {
				dot = sAccent.Render("◆")
			}
			lines = append(lines, markRow("side:manage:"+m.screen, item(m.screen == cur, dot, m.label), inner))
			if m.screen == cur && a.cur().name() == "Deploy" {
				lines = append(lines, "    "+sPink.Render("↳ ")+sSub.Render(truncate(a.deployLabel(), inner-6)))
			}
		}
	}
	lines = append(lines, "")

	// Servers
	profiles := config.Profiles()
	if len(profiles) == 0 {
		profiles = []config.Profile{{Name: config.DefaultProfile, Domain: a.cfg.Domain}}
	}
	showServers := sec == sideServers || sec == sideNone || len(profiles) > 1
	lines = append(lines, mark("side:head:servers", head(sideServers, "Servers", showServers)))
	if showServers {
		for _, p := range profiles {
			active := p.Name == config.Active()
			dot := sFaint.Render("○")
			if active {
				dot = sGood.Render("●")
			}
			label := p.Name
			if p.Domain != "" && p.Name == config.DefaultProfile {
				label = p.Domain
			}
			lines = append(lines, markRow("side:server:"+p.Name, item(active && sec == sideServers, dot, label), inner))
		}
	}
	return lines
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
