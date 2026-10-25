package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

var (
	cPurple = lipgloss.AdaptiveColor{Light: "#5A3FD9", Dark: "#9D7CFF"}
	cMint   = lipgloss.AdaptiveColor{Light: "#00976B", Dark: "#3EF0B0"}
	cPink   = lipgloss.AdaptiveColor{Light: "#C2306B", Dark: "#FF7AB6"}
	cAmber  = lipgloss.AdaptiveColor{Light: "#A86400", Dark: "#FFC56B"}
	cRed    = lipgloss.AdaptiveColor{Light: "#C4243F", Dark: "#FF6B81"}
	cText   = lipgloss.AdaptiveColor{Light: "#1E1B2E", Dark: "#E8E6F3"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#6E6A86", Dark: "#8A86A3"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#C9C5DD", Dark: "#3A3650"}
	cPanel  = lipgloss.AdaptiveColor{Light: "#F3F0FF", Dark: "#1B1828"}
)

var (
	sTitle   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sSub     = lipgloss.NewStyle().Foreground(cMuted)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sFaint   = lipgloss.NewStyle().Foreground(cFaint)
	sAccent  = lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	sGood    = lipgloss.NewStyle().Foreground(cMint)
	sWarn    = lipgloss.NewStyle().Foreground(cAmber)
	sBad     = lipgloss.NewStyle().Foreground(cRed)
	sPink    = lipgloss.NewStyle().Foreground(cPink)
	sKey     = lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	sSection = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	sPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cFaint).
		Padding(0, 1)
	sPanelFocus = sPanel.BorderForeground(cPurple)

	sBadge = lipgloss.NewStyle().Padding(0, 1).Bold(true)
)

func badge(text string, bg lipgloss.TerminalColor) string {
	return sBadge.Foreground(lipgloss.Color("#111018")).Background(bg).Render(text)
}

// gradient colours each rune of s along a purple → pink → mint ramp.
func gradient(s string) string {
	stops := []string{"#9D7CFF", "#FF7AB6", "#3EF0B0"}
	runes := []rune(s)
	n := 0
	for _, r := range runes {
		if r != ' ' && r != '\n' {
			n++
		}
	}
	var b strings.Builder
	i := 0
	for _, r := range runes {
		if r == ' ' || r == '\n' {
			b.WriteRune(r)
			continue
		}
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(ramp(stops, t))).Render(string(r)))
		i++
	}
	return b.String()
}

// gradientLines colours a multi-line block column-wise so each line shares the ramp.
func gradientLines(block string) string {
	stops := []string{"#9D7CFF", "#FF7AB6", "#3EF0B0"}
	lines := strings.Split(block, "\n")
	width := 0
	for _, l := range lines {
		width = max(width, len([]rune(l)))
	}
	for li, l := range lines {
		var b strings.Builder
		for ci, r := range []rune(l) {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			t := float64(ci) / float64(max(1, width-1))
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(ramp(stops, t))).Render(string(r)))
		}
		lines[li] = b.String()
	}
	return strings.Join(lines, "\n")
}

func ramp(stops []string, t float64) string {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	seg := t * float64(len(stops)-1)
	i := int(seg)
	a, _ := colorful.Hex(stops[i])
	z, _ := colorful.Hex(stops[i+1])
	return a.BlendLuv(z, seg-float64(i)).Clamped().Hex()
}

const logo = `
 ▀█▀ █▄ █ ▀█▀ █▀▀ █▀█ ▀▀█ █▀█ █▄ █ █▀▀
  █  █ ▀█  █  ██▄ █▀▄ ▄▀  █▄█ █ ▀█ ██▄`

func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// truncateMid shortens s to w runes by cutting its middle, so names that
// share a long prefix stay apart.
func truncateMid(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 5 {
		return truncate(s, w)
	}
	head := (w - 1) / 2
	return string(r[:head]) + "…" + string(r[len(r)-(w-1-head):])
}
