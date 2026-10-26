package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Kit ────────────────────────────────────────────────────────────
//
// The pieces the screens are drawn from, besides card and columns: chips
// for keys and tags, tiles for the menu, picked cards for the entry under
// the cursor and bullets grouped under small labels.

// chip is a short label on a filled background, like a key cap.
func chip(label string, fg, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Padding(0, 1).Render(label)
}

// keyChip shows a hotkey.
func keyChip(k string) string { return chip(k, cText, cFaint) }

// tagChip is a coloured tag, such as "needs settings" or "beta".
func tagChip(label string, fg lipgloss.TerminalColor) string { return chip(label, fg, cFaint) }

// picked boxes the entry under the cursor in the accent colour, w wide.
func picked(lines []string, w int) string {
	return card("", lines, w, 0, cPurple)
}

// tileGrid lays tiles out cols across, each row as tall as its tallest.
// Every tile is a func so columns can size it.
func tileGrid(tiles []func(w, h int) string, w, cols int) string {
	var rows []string
	for i := 0; i < len(tiles); i += cols {
		row := tiles[i:min(len(tiles), i+cols)]
		for len(row) < cols { // a short last row keeps the tiles' width
			row = append(row, func(w, h int) string { return lipgloss.NewStyle().Width(w).Render("") })
		}
		rows = append(rows, columns(w, row...))
	}
	return strings.Join(rows, "\n")
}

// menuTile is one tile of the compact menu: an icon and a label, and a
// second line when there's something going on.
func menuTile(id, icon, label, sub string, on bool) func(w, h int) string {
	return func(w, h int) string {
		name := sTitle.Render(truncate(label, w-8))
		border := lipgloss.TerminalColor(cFaint)
		if on {
			name, border = gradient(truncate(label, w-8)), cPurple
		}
		lines := []string{icon + "  " + name}
		if sub != "" {
			lines = append(lines, sPink.Render("↳ ")+sSub.Render(sub))
		}
		return markLines(id, card("", lines, w, h, border))
	}
}

// bulletGroup is a label and the items under it.
type bulletGroup struct {
	label string
	items []string
}

// bullets lists groups with their labels in a column on the left, or
// above the items when w is too narrow for that.
func bullets(groups []bulletGroup, w int) string {
	labelW := 0
	for _, g := range groups {
		labelW = max(labelW, lipgloss.Width(g.label))
	}
	side := w-labelW-4 >= 18
	var lines []string
	for _, g := range groups {
		if !side {
			lines = append(lines, sSection.Render(g.label))
		}
		for i, it := range g.items {
			label := ""
			if side {
				label = strings.Repeat(" ", labelW+2)
				if i == 0 {
					label = sMuted.Render(g.label) + strings.Repeat(" ", labelW+2-lipgloss.Width(g.label))
				}
			}
			lines = append(lines, label+sAccent.Render("• ")+sTitle.Render(truncate(it, w-lipgloss.Width(label)-2)))
		}
	}
	return strings.Join(lines, "\n")
}

// rule is a section heading followed by a faint line to the edge.
func rule(title string, w int) string {
	head := sSection.Render(title)
	if n := w - lipgloss.Width(head) - 1; n > 0 {
		head += " " + sFaint.Render(strings.Repeat("─", n))
	}
	return head
}
