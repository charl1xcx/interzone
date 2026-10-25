package ui

import (
	"os"
	"path/filepath"
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
	for _, k := range layouts {
		if l == k {
			return l
		}
	}
	return "auto"
}

// SetLayout forces a layout for this run without saving it, as the
// -layout flag does. Unknown names are ignored.
func (a *App) SetLayout(l string) {
	for _, k := range layouts {
		if l == k {
			a.layout = l
		}
	}
}

// cycleLayout switches to the next layout and remembers it.
func (a *App) cycleLayout() {
	for i, k := range layouts {
		if k == a.layout {
			a.layout = layouts[(i+1)%len(layouts)]
			break
		}
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

// layoutLink is the clickable switch shown in the sidebar and the menu.
func (a *App) layoutLink() string {
	l := a.layout
	if l == "auto" {
		l = "auto (wide)"
		if a.compact() {
			l = "auto (compact)"
		}
	}
	return mark("app:layout", sFaint.Render("◫ layout: ")+sMuted.Render(l))
}

func (a *App) compactView() string {
	w := a.w
	menu := mark("app:menu", sAccent.Render("≡ menu"))
	if a.menuOpen {
		menu = mark("app:menu", sAccent.Render("✕ close"))
	}
	mode := badge("ONLINE", cMint)
	if a.cfg.Sources.Blackout {
		mode = badge("BLACKOUT", cAmber)
	}
	if hb := a.healthBadge(true); hb != "" {
		mode = hb + " " + mode
	}
	brand := gradient("◢◤ interzone")
	gap := w - lipgloss.Width(brand) - lipgloss.Width(mode) - lipgloss.Width(menu) - 4
	header := " " + brand + strings.Repeat(" ", max(1, gap)) + mode + "  " + menu
	header = truncateANSI(header, w)

	sub := ""
	if a.step > 0 {
		sub = truncateANSI(a.stepper(w), w)
	}
	if a.menuOpen {
		sub = " " + sAccent.Render("◆ Menu")
	}
	rule := sFaint.Render(strings.Repeat("─", w))
	footer := a.compactFooter(w)

	bodyH := a.h - 2 - 2 - lipgloss.Height(footer) // header, stepper, two rules
	inner := w - 2
	var content string
	if a.menuOpen {
		lines := a.menuLines(inner)
		lines = append(lines, "", a.layoutLink())
		content = strings.Join(lines, "\n")
	} else {
		contentH := max(3, bodyH-2)
		content = a.cur().view(inner, contentH)
		if lines := strings.Split(content, "\n"); len(lines) > contentH {
			content = strings.Join(lines[:contentH], "\n")
		}
		content = sTitle.Render(a.cur().title()) + "\n\n" + content
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
	return zone.Scan(strings.Join([]string{header, sub, rule, body, rule, footer}, "\n"))
}

// compactFooter wraps the key hints over up to three lines.
func (a *App) compactFooter(w int) string {
	if a.status != "" {
		return lipgloss.NewStyle().Width(w).Render(" " + a.status)
	}
	var parts []string
	if a.menuOpen {
		parts = []string{hint("esc", "close"), hint("ctrl+c", "quit")}
	} else {
		for _, k := range a.cur().keys() {
			parts = append(parts, hint(k[0], k[1]))
		}
		if a.step > 0 && !a.escListed() {
			parts = append(parts, hint("esc", "back"))
		}
		parts = append(parts, hint("ctrl+c", "quit"))
	}
	sep := sFaint.Render(" · ")
	var lines []string
	line := ""
	for _, p := range parts {
		switch {
		case line == "":
			line = " " + p
		case lipgloss.Width(line+sep+p) <= w:
			line += sep + p
		default:
			lines = append(lines, line)
			line = " " + p
		}
	}
	lines = append(lines, line)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	for i, l := range lines {
		lines[i] = truncateANSI(l, w)
	}
	return strings.Join(lines, "\n")
}
