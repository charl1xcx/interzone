package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"interzone/internal/config"
)

// ── Layout ─────────────────────────────────────────────────────────
//
// The wide layout has the menu on the left and a bordered panel. The
// compact one is for phones (Termux) and other narrow or portrait
// terminals: no sidebar and no border, the menu opens over the screen from
// the ≡ in the header, and the key hints wrap instead of being cut off.
// "auto" picks compact when the terminal is narrow or taller than wide.

var layouts = []string{"auto", "compact", "wide"}

func layoutFile() string { return filepath.Join(config.Dir(), "layout") }

func loadLayout() string {
	b, _ := os.ReadFile(layoutFile())
	l := strings.TrimSpace(string(b))
	if slices.Contains(layouts, l) {
		return l
	}
	return "auto"
}

// SetLayout forces a layout for this run without saving it, as the
// -layout flag does. Unknown names are ignored.
func (a *App) SetLayout(l string) {
	if slices.Contains(layouts, l) {
		a.layout = l
	}
}

// cycleLayout switches to the next layout and remembers it.
func (a *App) cycleLayout() {
	if i := slices.Index(layouts, a.layout); i >= 0 {
		a.layout = layouts[(i+1)%len(layouts)]
	}
	if a.layout == "auto" {
		_ = os.Remove(layoutFile())
	} else {
		_ = os.MkdirAll(config.Dir(), 0o700)
		_ = os.WriteFile(layoutFile(), []byte(a.layout+"\n"), 0o600)
	}
	a.menuOpen = a.menuOpen && a.compact()
}

func (a *App) compact() bool {
	switch a.layout {
	case "compact":
		return true
	case "wide":
		return false
	}
	// A cell is about twice as tall as wide, so w < 2h is a portrait window.
	return a.w < 80 || (a.w < 100 && a.w < 2*a.h)
}

// layoutName is the layout, with what auto picked.
func (a *App) layoutName() string {
	l := a.layout
	if l == "auto" {
		l = "auto (wide)"
		if a.compact() {
			l = "auto (compact)"
		}
	}
	return l
}

// menuLink is one of the links under the menu (layout, theme, fold), to
// click or select from the keys like its entries.
func (a *App) menuLink(id, glyph, text, hint string) string {
	g, t := sFaint.Render(glyph), sMuted.Render(text)
	if a.focused(id) {
		g, t = menuMarker(), sFocus.Render(text)
	}
	return mark(id, g+" "+t+sFaint.Render(hint))
}

// tight is set while a compact screen is drawn on a short terminal, such
// as a phone with its keyboard up: screens drop blank lines and show
// only the selected entry in full.
var tight bool

// compactTightH is the height below which the compact layout is tight.
const compactTightH = 26

func (a *App) compactView() string {
	w := a.w
	tight = a.h < compactTightH
	defer func() { tight = false }()
	header := a.compactHeader(w)
	bar := a.compactBar(w)
	status := ""
	if a.status != "" {
		status = truncateANSI(" "+a.status, w)
	}

	bodyH := a.h - 1 - lipgloss.Height(bar)
	if status != "" {
		bodyH--
	}
	inner := w - 2
	var content string
	switch {
	case a.menuOpen:
		content = a.menuScrolled(a.menuTiles(inner)+"\n"+a.menuLink("app:layout", "◫", a.layoutName(), "")+"     "+a.menuLink("app:theme", "◐", themeName, ""), bodyH)
	case a.sheetOpen:
		content = a.sheetView(inner)
	default:
		gap := "\n\n"
		if tight {
			gap = "\n"
		}
		title := sTitle.Render(truncate(a.cur().title(), inner))
		contentH := max(3, bodyH-lipgloss.Height(title)-len(gap)+1)
		content = a.screenView(inner, contentH)
		if lines := strings.Split(content, "\n"); len(lines) > contentH {
			content = strings.Join(lines[:contentH], "\n")
		}
		content = title + gap + content
	}
	lines := strings.Split(content, "\n")
	if len(lines) > bodyH {
		lines = lines[:max(0, bodyH)]
	}
	for i, l := range lines {
		lines[i] = truncateANSI(l, inner) // a line too wide would wrap and shift the rest
	}
	content = strings.Join(lines, "\n")
	body := lipgloss.NewStyle().Padding(0, 1).Width(w).Height(max(1, bodyH)).Render(content)
	parts := []string{header, body}
	if status != "" {
		parts = append(parts, status)
	}
	return zone.Scan(strings.Join(append(parts, bar), "\n"))
}

// menuScrolled cuts the menu to h lines from a.menuTop, which the wheel
// and ↑↓ move.
func (a *App) menuScrolled(menu string, h int) string {
	lines := strings.Split(menu, "\n")
	if len(lines) <= h {
		a.menuTop = 0
		return menu
	}
	if a.menuFollow && a.menuFocusLine >= 0 {
		// Keep the selection in view, clear of the ▲/▼ lines; a tile is
		// three lines tall.
		a.menuTop = min(a.menuTop, a.menuFocusLine-1)
		a.menuTop = max(a.menuTop, a.menuFocusLine+4-h)
	}
	a.menuFollow = false
	a.menuTop = max(0, min(a.menuTop, len(lines)-h))
	view := append([]string(nil), lines[a.menuTop:a.menuTop+h]...)
	if a.menuTop > 0 {
		view[0] = sFaint.Render("▲ more above")
	}
	if a.menuTop+h < len(lines) {
		view[h-1] = sFaint.Render("▼ more below")
	}
	return strings.Join(view, "\n")
}

// compactHeader is one line: the brand, where you are, and the health.
func (a *App) compactHeader(w int) string {
	brand := gradient("◢◤")
	right := ""
	if a.cfg.Sources.Blackout {
		right = badge("BLACKOUT", cAmber)
	}
	if hb := a.healthBadge(true); hb != "" {
		right = strings.TrimSpace(hb + " " + right)
	}
	where := ""
	switch {
	case a.menuOpen:
		where = sAccent.Render("Menu")
	case a.sheetOpen:
		where = sAccent.Render("Keys")
	case a.step == 0:
		where = gradient("interzone")
	default:
		where = a.compactWhere()
	}
	room := w - 4 - lipgloss.Width(right)
	where = truncateANSI(where, max(4, room))
	gap := w - 3 - lipgloss.Width(where) - lipgloss.Width(right) - 1
	return truncateANSI(" "+brand+" "+where+strings.Repeat(" ", max(1, gap))+right, w)
}

// compactWhere names the screen: "2/4 Homeserver", "Manage › Users".
func (a *App) compactWhere() string {
	name := a.cur().name()
	switch a.section() {
	case sideManage:
		if name == "Manage" {
			return sAccent.Render("◆ Manage")
		}
		if name == "Deploy" {
			name = a.screens[a.deployBack].name()
		}
		return sAccent.Render("◆ ") + sMuted.Render("Manage › ") + sTitle.Render(name)
	case sideMirrors:
		return sAccent.Render("⇅ Mirrors")
	case sideTest:
		return sAccent.Render("⌖ Test")
	case sideServers:
		return sAccent.Render("▤ Servers")
	}
	steps := a.wizard()
	return sAccent.Render(fmt.Sprintf("%d/%d ", a.step, len(steps))) + sTitle.Render(name)
}

// button is one of the bottom bar's tap targets, w columns wide.
func button(id, label string, w int, primary bool) string {
	st := lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Foreground(cText).Background(cFaint)
	if primary {
		st = st.Foreground(cOnAccent).Background(cPurple).Bold(true)
	}
	return mark(id, st.Render(truncate(label, w)))
}

// barButton is a button: its zone id and label.
type barButton struct {
	id, label string
	primary   bool
}

// compactBar is the row of buttons at the bottom, in reach of a thumb:
// back, the menu, the screen's main action, and ⋯ for every other key.
func (a *App) compactBar(w int) string {
	var bs []barButton
	switch {
	case a.menuOpen || a.sheetOpen:
		bs = append(bs, barButton{"key:esc", "✕ close", false})
	default:
		if a.step > 0 {
			label := "back"
			for _, k := range a.cur().keys() {
				if k[0] == "esc" {
					label = k[1]
				}
			}
			bs = append(bs, barButton{"key:esc", "‹ " + label, false})
		}
		bs = append(bs, barButton{"app:menu", "≡ menu", false})
		for _, k := range a.cur().keys() {
			if k[0] == "enter" {
				bs = append(bs, barButton{"key:enter", k[1] + " ›", true})
			}
		}
		bs = append(bs, barButton{"app:sheet", "⋯", false})
	}
	// ⋯ is narrow; the others share the rest.
	n := len(bs)
	gaps := n - 1
	moreW := 0
	if bs[n-1].id == "app:sheet" {
		moreW = 5
	}
	each := (w - 2 - gaps - moreW) / max(1, n-boolInt(moreW > 0))
	var out []string
	used := 0
	for i, b := range bs {
		bw := each
		switch {
		case b.id == "app:sheet":
			bw = moreW
		case i == n-1 || (i == n-2 && moreW > 0):
			bw = w - 2 - gaps - moreW - used // the last wide one takes what's left
		}
		used += bw
		out = append(out, button(b.id, b.label, bw, b.primary))
	}
	return " " + strings.Join(out, " ")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sheetView lists every key the screen takes, each a row to tap, plus the
// ones that work everywhere.
func (a *App) sheetView(inner int) string {
	row := func(key, desc string) []string {
		label := sKey.Render(fmt.Sprintf("%-8s", key)) + " " + sTitle.Render(desc)
		if _, ok := keyMsg(key); !ok {
			return []string{"  " + sFaint.Render(fmt.Sprintf("%-8s", key)) + " " + sMuted.Render(desc)}
		}
		return []string{markRow("key:"+key, "  "+label, inner)}
	}
	var lines []string
	lines = append(lines, sSection.Render("On this screen"))
	for _, k := range a.cur().keys() {
		if k[0] == "enter" || k[0] == "esc" {
			continue // on the bar
		}
		lines = append(lines, row(k[0], k[1])...)
		if !tight {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "", sSection.Render("Anywhere"))
	for _, k := range [][2]string{{"ctrl+s", "save answers"}, {"ctrl+l", "layout: " + a.layoutName()}, {"ctrl+y", "theme: " + themeName}, {"ctrl+c", "quit"}} {
		lines = append(lines, row(k[0], k[1])...)
		if !tight {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}
