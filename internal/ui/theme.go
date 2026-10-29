package ui

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"

	"interzone/internal/config"
)

// ── Themes ─────────────────────────────────────────────────────────
//
// The colours below are the current theme's; applyTheme swaps them and
// rebuilds the styles made from them. ctrl+y cycles the themes and the
// choice is kept in config.Dir()/theme.

type ac = lipgloss.AdaptiveColor

type theme struct {
	name                                                      string
	accent, good, pink, amber, red, text, muted, faint, panel ac
	onAccent                                                  ac // text on a badge
}

var themes = []theme{
	{"interzone",
		ac{Light: "#5A3FD9", Dark: "#9D7CFF"}, ac{Light: "#00976B", Dark: "#3EF0B0"}, ac{Light: "#C2306B", Dark: "#FF7AB6"},
		ac{Light: "#A86400", Dark: "#FFC56B"}, ac{Light: "#C4243F", Dark: "#FF6B81"}, ac{Light: "#1E1B2E", Dark: "#E8E6F3"},
		ac{Light: "#6E6A86", Dark: "#8A86A3"}, ac{Light: "#C9C5DD", Dark: "#3A3650"}, ac{Light: "#F3F0FF", Dark: "#1B1828"},
		ac{Light: "#111018", Dark: "#111018"}},
	{"catppuccin",
		ac{Light: "#8839EF", Dark: "#CBA6F7"}, ac{Light: "#40A02B", Dark: "#A6E3A1"}, ac{Light: "#EA76CB", Dark: "#F5C2E7"},
		ac{Light: "#DF8E1D", Dark: "#F9E2AF"}, ac{Light: "#D20F39", Dark: "#F38BA8"}, ac{Light: "#4C4F69", Dark: "#CDD6F4"},
		ac{Light: "#8C8FA1", Dark: "#7F849C"}, ac{Light: "#BCC0CC", Dark: "#45475A"}, ac{Light: "#EFF1F5", Dark: "#1E1E2E"},
		ac{Light: "#EFF1F5", Dark: "#11111B"}},
	{"nord",
		ac{Light: "#5E81AC", Dark: "#88C0D0"}, ac{Light: "#4F7A3A", Dark: "#A3BE8C"}, ac{Light: "#8F5E86", Dark: "#B48EAD"},
		ac{Light: "#B5891F", Dark: "#EBCB8B"}, ac{Light: "#A3434C", Dark: "#BF616A"}, ac{Light: "#2E3440", Dark: "#ECEFF4"},
		ac{Light: "#4C566A", Dark: "#7B88A1"}, ac{Light: "#D8DEE9", Dark: "#434C5E"}, ac{Light: "#ECEFF4", Dark: "#2E3440"},
		ac{Light: "#ECEFF4", Dark: "#2E3440"}},
	{"gruvbox",
		ac{Light: "#AF3A03", Dark: "#FE8019"}, ac{Light: "#79740E", Dark: "#B8BB26"}, ac{Light: "#8F3F71", Dark: "#D3869B"},
		ac{Light: "#B57614", Dark: "#FABD2F"}, ac{Light: "#9D0006", Dark: "#FB4934"}, ac{Light: "#3C3836", Dark: "#EBDBB2"},
		ac{Light: "#7C6F64", Dark: "#A89984"}, ac{Light: "#D5C4A1", Dark: "#504945"}, ac{Light: "#FBF1C7", Dark: "#282828"},
		ac{Light: "#FBF1C7", Dark: "#282828"}},
	{"dracula",
		ac{Light: "#644AC9", Dark: "#BD93F9"}, ac{Light: "#14710A", Dark: "#50FA7B"}, ac{Light: "#A3144D", Dark: "#FF79C6"},
		ac{Light: "#846E15", Dark: "#F1FA8C"}, ac{Light: "#CB3A2A", Dark: "#FF5555"}, ac{Light: "#1F1F1F", Dark: "#F8F8F2"},
		ac{Light: "#635D97", Dark: "#6272A4"}, ac{Light: "#CFCFDE", Dark: "#44475A"}, ac{Light: "#FFFBEB", Dark: "#282A36"},
		ac{Light: "#FFFBEB", Dark: "#282A36"}},
	{"solarized",
		ac{Light: "#268BD2", Dark: "#268BD2"}, ac{Light: "#859900", Dark: "#859900"}, ac{Light: "#D33682", Dark: "#D33682"},
		ac{Light: "#B58900", Dark: "#B58900"}, ac{Light: "#DC322F", Dark: "#DC322F"}, ac{Light: "#586E75", Dark: "#93A1A1"},
		ac{Light: "#93A1A1", Dark: "#657B83"}, ac{Light: "#EEE8D5", Dark: "#073642"}, ac{Light: "#FDF6E3", Dark: "#002B36"},
		ac{Light: "#FDF6E3", Dark: "#002B36"}},
	{"tokyonight",
		ac{Light: "#2E7DE9", Dark: "#7AA2F7"}, ac{Light: "#587539", Dark: "#9ECE6A"}, ac{Light: "#9854F1", Dark: "#BB9AF7"},
		ac{Light: "#8C6C3E", Dark: "#E0AF68"}, ac{Light: "#F52A65", Dark: "#F7768E"}, ac{Light: "#3760BF", Dark: "#C0CAF5"},
		ac{Light: "#6172B0", Dark: "#737AA2"}, ac{Light: "#C4C8DA", Dark: "#3B4261"}, ac{Light: "#E1E2E7", Dark: "#1A1B26"},
		ac{Light: "#E1E2E7", Dark: "#1A1B26"}},
	{"rose-pine",
		ac{Light: "#907AA9", Dark: "#C4A7E7"}, ac{Light: "#56949F", Dark: "#9CCFD8"}, ac{Light: "#D7827E", Dark: "#EBBCBA"},
		ac{Light: "#EA9D34", Dark: "#F6C177"}, ac{Light: "#B4637A", Dark: "#EB6F92"}, ac{Light: "#575279", Dark: "#E0DEF4"},
		ac{Light: "#797593", Dark: "#908CAA"}, ac{Light: "#DFDAD9", Dark: "#403D52"}, ac{Light: "#FAF4ED", Dark: "#191724"},
		ac{Light: "#FAF4ED", Dark: "#191724"}},
	{"everforest",
		ac{Light: "#8DA101", Dark: "#A7C080"}, ac{Light: "#35A77C", Dark: "#83C092"}, ac{Light: "#DF69BA", Dark: "#D699B6"},
		ac{Light: "#DFA000", Dark: "#DBBC7F"}, ac{Light: "#F85552", Dark: "#E67E80"}, ac{Light: "#5C6A72", Dark: "#D3C6AA"},
		ac{Light: "#939F91", Dark: "#859289"}, ac{Light: "#E6E2CC", Dark: "#475258"}, ac{Light: "#FDF6E3", Dark: "#2D353B"},
		ac{Light: "#FDF6E3", Dark: "#2D353B"}},
	{"kanagawa",
		ac{Light: "#4D699B", Dark: "#7E9CD8"}, ac{Light: "#6F894E", Dark: "#98BB6C"}, ac{Light: "#B35B79", Dark: "#D27E99"},
		ac{Light: "#77713F", Dark: "#E6C384"}, ac{Light: "#C84053", Dark: "#E46876"}, ac{Light: "#545464", Dark: "#DCD7BA"},
		ac{Light: "#8A8980", Dark: "#727169"}, ac{Light: "#D5CEA3", Dark: "#363646"}, ac{Light: "#F2ECBC", Dark: "#1F1F28"},
		ac{Light: "#F2ECBC", Dark: "#1F1F28"}},
	{"onedark",
		ac{Light: "#4078F2", Dark: "#61AFEF"}, ac{Light: "#50A14F", Dark: "#98C379"}, ac{Light: "#A626A4", Dark: "#C678DD"},
		ac{Light: "#C18401", Dark: "#E5C07B"}, ac{Light: "#E45649", Dark: "#E06C75"}, ac{Light: "#383A42", Dark: "#ABB2BF"},
		ac{Light: "#696C77", Dark: "#7F848E"}, ac{Light: "#D3D3D4", Dark: "#3E4451"}, ac{Light: "#FAFAFA", Dark: "#282C34"},
		ac{Light: "#FAFAFA", Dark: "#282C34"}},
	{"synthwave",
		ac{Light: "#00838F", Dark: "#36F9F6"}, ac{Light: "#1B8A5A", Dark: "#72F1B8"}, ac{Light: "#C2185B", Dark: "#FF7EDB"},
		ac{Light: "#9A6B00", Dark: "#FEDE5D"}, ac{Light: "#C62828", Dark: "#FE4450"}, ac{Light: "#241B2F", Dark: "#F4EEE4"},
		ac{Light: "#6B6390", Dark: "#848BBD"}, ac{Light: "#D9D2EA", Dark: "#495495"}, ac{Light: "#F5F0FF", Dark: "#262335"},
		ac{Light: "#F5F0FF", Dark: "#262335"}},
	{"mono",
		ac{Light: "#000000", Dark: "#FFFFFF"}, ac{Light: "#303030", Dark: "#D0D0D0"}, ac{Light: "#202020", Dark: "#E0E0E0"},
		ac{Light: "#505050", Dark: "#B0B0B0"}, ac{Light: "#000000", Dark: "#FFFFFF"}, ac{Light: "#1A1A1A", Dark: "#E0E0E0"},
		ac{Light: "#6A6A6A", Dark: "#8A8A8A"}, ac{Light: "#C8C8C8", Dark: "#3A3A3A"}, ac{Light: "#F2F2F2", Dark: "#1A1A1A"},
		ac{Light: "#FFFFFF", Dark: "#000000"}},
}

var (
	themeName                                                             string
	cPurple, cMint, cPink, cAmber, cRed, cText, cMuted, cFaint, cOnAccent ac

	sTitle, sSub, sMuted, sFaint, sAccent, sGood, sWarn, sBad, sPink, sKey, sSection lipgloss.Style
	sPanel, sPanelFocus, sBadge                                                      lipgloss.Style
)

func init() { applyTheme(themes[0]) }

func applyTheme(t theme) {
	themeName = t.name
	cPurple, cMint, cPink, cAmber, cRed = t.accent, t.good, t.pink, t.amber, t.red
	cText, cMuted, cFaint, cOnAccent = t.text, t.muted, t.faint, t.onAccent

	sTitle = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sSub = lipgloss.NewStyle().Foreground(cMuted)
	sMuted = lipgloss.NewStyle().Foreground(cMuted)
	sFaint = lipgloss.NewStyle().Foreground(cFaint)
	sAccent = lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	sGood = lipgloss.NewStyle().Foreground(cMint)
	sWarn = lipgloss.NewStyle().Foreground(cAmber)
	sBad = lipgloss.NewStyle().Foreground(cRed)
	sPink = lipgloss.NewStyle().Foreground(cPink)
	sKey = lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	sSection = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	sPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cFaint).
		Padding(0, 1)
	sPanelFocus = sPanel.BorderForeground(cPurple)

	sBadge = lipgloss.NewStyle().Padding(0, 1).Bold(true)
}

func themeFile() string { return filepath.Join(config.Dir(), "theme") }

// loadTheme applies the saved theme, if any.
func loadTheme() {
	b, _ := os.ReadFile(themeFile())
	name := strings.TrimSpace(string(b))
	for _, t := range themes {
		if t.name == name {
			applyTheme(t)
		}
	}
}

// SetTheme applies a theme for this run without saving it, as the -theme
// flag does. It reports whether the name is known.
func SetTheme(name string) bool {
	for _, t := range themes {
		if t.name == name {
			applyTheme(t)
			return true
		}
	}
	return false
}

// ThemeNames lists the themes, for -help.
func ThemeNames() []string {
	var out []string
	for _, t := range themes {
		out = append(out, t.name)
	}
	return out
}

// cycleTheme switches to the next theme and remembers it.
func cycleTheme() {
	next := themes[0]
	for i, t := range themes {
		if t.name == themeName {
			next = themes[(i+1)%len(themes)]
		}
	}
	applyTheme(next)
	if next.name == themes[0].name {
		_ = os.Remove(themeFile())
	} else {
		_ = os.MkdirAll(config.Dir(), 0o700)
		_ = os.WriteFile(themeFile(), []byte(next.name+"\n"), 0o600)
	}
}

func badge(text string, bg lipgloss.TerminalColor) string {
	return sBadge.Foreground(cOnAccent).Background(bg).Render(text)
}

// gradientStops is the logo ramp: the accent through pink to the good colour.
func gradientStops() []string {
	pick := func(c ac) string {
		if lipgloss.HasDarkBackground() {
			return c.Dark
		}
		return c.Light
	}
	if themeName == themes[0].name {
		return []string{"#9D7CFF", "#FF7AB6", "#3EF0B0"} // as it always was, on any background
	}
	return []string{pick(cPurple), pick(cPink), pick(cMint)}
}

// gradient colours each character of s along the theme's ramp. It steps by
// grapheme, so an emoji isn't split from its variation selector: apart,
// they'd be measured narrower than the terminal draws them.
func gradient(s string) string {
	stops := gradientStops()
	var chars []string
	for g := uniseg.NewGraphemes(s); g.Next(); {
		chars = append(chars, g.Str())
	}
	n := 0
	for _, r := range chars {
		if r != " " && r != "\n" {
			n++
		}
	}
	var b strings.Builder
	i := 0
	for _, r := range chars {
		if r == " " || r == "\n" {
			b.WriteString(r)
			continue
		}
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(ramp(stops, t))).Render(r))
		i++
	}
	return b.String()
}

// gradientLinesAt colours a multi-line block column-wise so each line
// shares the ramp, moved along by shift: it runs there and back, so any
// shift is a smooth step from the last.
func gradientLinesAt(block string, shift float64) string {
	stops := gradientStops()
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
			t := math.Mod(float64(ci)/float64(max(1, width-1))+shift, 2)
			if t > 1 {
				t = 2 - t
			}
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

// plain is plain mode, for screen readers and logs: no colours, and the
// box, bar and block characters drawn in ASCII.
var plain bool

// SetPlain turns plain mode on, before the app starts.
func SetPlain() {
	plain = true
	lipgloss.SetColorProfile(termenv.Ascii)
}

// plainText swaps the drawing characters for ASCII ones of the same width;
// sparklines become digits, 1 low to 8 full.
var plainText = strings.NewReplacer(
	"╭", "+", "╮", "+", "╰", "+", "╯", "+", "┌", "+", "┐", "+", "└", "+", "┘", "+", "├", "+", "┤", "+", "┬", "+", "┴", "+", "┼", "+",
	"│", "|", "─", "-", "▌", ">", "░", ".", "▸", ">", "▾", "v", "›", ">", "●", "*", "○", "o", "◉", "*",
	"▁", "1", "▂", "2", "▃", "3", "▄", "4", "▅", "5", "▆", "6", "▇", "7", "█", "8",
	"◢◤", "  ", "▲", "^", "▼", "v", "◂", "<", "⊘", "x",
	// Icons only decorate, and a screen reader would read each one out.
	"▦", " ", "✎", " ", "⚿", " ", "⚑", " ", "◷", " ", "⌂", " ", "⇢", " ", "↻", " ", "↺", " ", "↶", " ", "⚒", " ", "◫", " ", "◐", " ", "▤", " ", "◆", " ", "◇", " ", "◈", " ", "◎", " ", "◔", " ", "▣", " ", "⏻", " ", "⌫", " ", "⊕", " ", "≡", " ", "≈", " ", "⇲", " ", "⇪", " ", "⇡", " ", "⇄", " ", "↯", " ", "⇩", " ",
)

const logo = `
 ▀█▀ █▄ █ ▀█▀ █▀▀ █▀█ ▀▀█ █▀█ █▄ █ █▀▀
  █  █ ▀█  █  ██▄ █▀▄ ▄▀  █▄█ █ ▀█ ██▄`

func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…") // by width: CJK and emoji take two cells
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
