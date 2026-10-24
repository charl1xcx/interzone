// Package ui is the bubbletea front-end: a step-by-step wizard that ends in
// a live deployment view.
package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
)

type screen interface {
	name() string // short name for the stepper
	title() string
	enter(c *config.Config) tea.Cmd
	leave(c *config.Config)
	update(msg tea.Msg) tea.Cmd
	view(w, h int) string
	keys() [][2]string
	typing() bool // printable keys go to an input
}

type navMsg int

func nav(dir int) tea.Cmd { return func() tea.Msg { return navMsg(dir) } }

type App struct {
	cfg     *config.Config
	loaded  bool
	screens []screen
	step    int
	w, h    int
	status  string
}

func New(cfg *config.Config, loaded bool) *App {
	a := &App{cfg: cfg, loaded: loaded}
	a.screens = []screen{
		&welcomeScreen{loaded: loaded},
		newNetworkScreen(),
		newServerScreen(),
		&homeserverScreen{},
		newSSLScreen(),
		newComponentsScreen(),
		&settingsScreen{},
		newReviewScreen(),
		newDeployScreen(),
	}
	return a
}

func (a *App) Init() tea.Cmd { return a.screens[0].enter(a.cfg) }

func (a *App) cur() screen { return a.screens[a.step] }

func (a *App) go_(dir int) tea.Cmd {
	n := a.step + dir
	if n < 0 || n >= len(a.screens) {
		return nil
	}
	a.cur().leave(a.cfg)
	if err := a.cfg.Save(); err != nil {
		a.status = "couldn't save config: " + err.Error()
	}
	a.step = n
	return a.cur().enter(a.cfg)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
	case navMsg:
		return a, a.go_(int(m))
	case formDoneMsg:
		return a, a.go_(1)
	case tea.KeyMsg:
		a.status = ""
		ds, deploying := a.cur().(*deployScreen)
		switch m.String() {
		case "ctrl+c":
			if deploying && ds.running {
				ds.abort()
				return a, nil
			}
			a.cur().leave(a.cfg)
			_ = a.cfg.Save()
			return a, tea.Quit
		case "esc":
			if deploying && ds.running {
				return a, nil
			}
			if cs, ok := a.cur().(*componentsScreen); ok && (cs.filtering || cs.filter.Value() != "") {
				break
			}
			return a, a.go_(-1)
		case "ctrl+n", "pgdown":
			if a.step < len(a.screens)-2 {
				return a, a.go_(1)
			}
		case "ctrl+p", "pgup":
			if !(deploying && ds.running) {
				return a, a.go_(-1)
			}
		case "ctrl+s":
			a.cur().leave(a.cfg)
			if err := a.cfg.Save(); err != nil {
				a.status = sBad.Render("save failed: " + err.Error())
			} else {
				a.status = sGood.Render("✓ answers saved to " + config.Dir())
			}
			return a, nil
		case "q":
			if !a.cur().typing() && !(deploying && ds.running) {
				a.cur().leave(a.cfg)
				_ = a.cfg.Save()
				return a, tea.Quit
			}
		}
	}
	return a, a.cur().update(msg)
}

func (a *App) View() string {
	if a.w == 0 {
		return ""
	}
	w := a.w
	header := a.header(w)
	stepper := a.stepper(w)
	footer := a.footer(w)

	bodyH := a.h - lipgloss.Height(header) - lipgloss.Height(stepper) - lipgloss.Height(footer) - 2
	inner := w - 4
	title := sTitle.Render(a.cur().title())
	content := a.cur().view(inner, max(3, bodyH-3))
	body := sPanelFocus.Width(w - 2).Height(max(3, bodyH)).Render(title + "\n\n" + content)

	return lipgloss.JoinVertical(lipgloss.Left, header, stepper, body, footer)
}

func (a *App) header(w int) string {
	brand := gradient("◢◤ interzone") + sMuted.Render("  matrix, deployed anywhere")
	mode := badge("ONLINE", cMint)
	if a.cfg.Sources.Blackout {
		mode = badge("BLACKOUT-READY", cAmber)
	}
	gap := w - lipgloss.Width(brand) - lipgloss.Width(mode) - 2
	return " " + brand + strings.Repeat(" ", max(1, gap)) + mode
}

func (a *App) stepper(w int) string {
	var parts []string
	for i, s := range a.screens[1:] {
		idx := i + 1
		switch {
		case idx == a.step:
			parts = append(parts, sAccent.Render("◉ "+s.name()))
		case idx < a.step:
			parts = append(parts, sGood.Render("●")+sMuted.Render(" "+s.name()))
		default:
			parts = append(parts, sFaint.Render("○ "+s.name()))
		}
	}
	line := " " + strings.Join(parts, sFaint.Render(" ─ "))
	if lipgloss.Width(line) > w {
		// Too narrow: show dots plus the current step's name.
		var dots []string
		for i := range a.screens[1:] {
			switch {
			case i+1 == a.step:
				dots = append(dots, sAccent.Render("◉"))
			case i+1 < a.step:
				dots = append(dots, sGood.Render("●"))
			default:
				dots = append(dots, sFaint.Render("○"))
			}
		}
		line = " " + strings.Join(dots, " ")
		if a.step > 0 {
			line += "  " + sAccent.Render(a.cur().name())
		}
	}
	return line
}

func (a *App) footer(w int) string {
	var parts []string
	for _, k := range a.cur().keys() {
		parts = append(parts, sKey.Render(k[0])+" "+sMuted.Render(k[1]))
	}
	if a.step > 0 {
		parts = append(parts, sKey.Render("esc")+" "+sMuted.Render("back"))
	}
	parts = append(parts, sKey.Render("ctrl+c")+" "+sMuted.Render("quit"))
	line := " " + strings.Join(parts, sFaint.Render(" · "))
	if a.status != "" {
		line = " " + a.status
	}
	return truncateANSI(line, w)
}

func truncateANSI(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
