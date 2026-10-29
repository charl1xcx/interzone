package ui

import (
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// ── Motion ─────────────────────────────────────────────────────────
//
// A few things move: Welcome's logo flows through the theme's colours and
// the deploy bar shimmers while it runs. Frames tick only while the screen
// showing asks for them, and never in plain mode. Frame 0 is the still
// picture, which the layout tests draw.

var animFrame int

type animTickMsg struct{}

const animEvery = 100 * time.Millisecond

// animator is a screen that moves while animating says so.
type animator interface{ animating() bool }

// animate asks for the next frame if the screen showing moves and no tick
// is on its way already.
func (a *App) animate() tea.Cmd {
	an, ok := a.cur().(animator)
	if a.animOn || plain || !ok || !an.animating() {
		return nil
	}
	a.animOn = true
	return tea.Tick(animEvery, func(time.Time) tea.Msg { return animTickMsg{} })
}

func (s *welcomeScreen) animating() bool { return true }

// flowBar is a progress bar w wide in the theme's ramp; while running, a
// highlight sweeps along its filled part.
func flowBar(frac float64, w int, running bool) string {
	n := int(max(0, min(1, frac))*float64(w) + 0.5)
	stops := gradientStops()
	hi := -100.0
	if running && n > 0 {
		hi = float64(animFrame%(n+12) - 6) // starts off the bar, so frame 0 is plain
	}
	white, _ := colorful.Hex("#FFFFFF")
	var b strings.Builder
	for i := range n {
		c, _ := colorful.Hex(ramp(stops, float64(i)/float64(max(1, w-1))))
		if d := math.Abs(float64(i) - hi); d < 4 {
			c = c.BlendLuv(white, 0.55*(1-d/4))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c.Clamped().Hex())).Render("█"))
	}
	return b.String() + sFaint.Render(strings.Repeat("░", w-n))
}
