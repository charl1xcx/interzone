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

// toolScreen marks screens reached from Manage; esc returns there.
type toolScreen interface{ tool() }

// escHandler screens use esc themselves (to close an editor or a
// confirmation) while handlesEsc reports true.
type escHandler interface{ handlesEsc() bool }

type navMsg int

func nav(dir int) tea.Cmd { return func() tea.Msg { return navMsg(dir) } }

type App struct {
	cfg     *config.Config
	loaded  bool
	screens []screen
	step    int
	w, h    int
	status  string

	deployBack int // where esc leads from the Deploy screen
	// tuningFromManage: Tuning was opened from Manage, so esc returns there.
	tuningFromManage bool
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
		newTuningScreen(),
		newReviewScreen(),
		newDeployScreen(),
		newManageScreen(), // not a wizard step: reached from Welcome
		// Reached from Manage:
		newUpdatesScreen(),
		newHistoryScreen(),
		newRestoreScreen(),
		newBackupsScreen(),
		newUsersScreen(),
		newDashboardScreen(),
		newServersScreen(),
		newMigrateScreen(),
	}
	a.deployBack = a.index("Review")
	return a
}

func (a *App) index(name string) int {
	for i, s := range a.screens {
		if s.name() == name {
			return i
		}
	}
	return 0
}

// wizard is the screens shown in the stepper, Welcome and Manage excluded.
func (a *App) wizard() []screen { return a.screens[1:a.index("Manage")] }

func (a *App) Init() tea.Cmd { return a.screens[0].enter(a.cfg) }

func (a *App) cur() screen { return a.screens[a.step] }

func (a *App) go_(dir int) tea.Cmd {
	return a.jump(a.step + dir)
}

func (a *App) jump(n int) tea.Cmd {
	if n < 0 || n >= len(a.screens) || n == a.step {
		return nil
	}
	if a.screens[n].name() == "Deploy" {
		a.deployBack = a.step
	}
	if a.screens[n].name() == "Tuning" {
		a.tuningFromManage = a.cur().name() == "Manage"
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
		if _, ok := a.cur().(*manageScreen); ok {
			return a, a.cur().update(m)
		}
		if _, ok := a.cur().(toolScreen); ok {
			return a, a.cur().update(m)
		}
		return a, a.go_(1)
	case switchServerMsg:
		return a, a.switchServer(m)
	case gotoMsg:
		return a, a.jump(a.index(string(m)))
	case runActionMsg:
		cmd := a.jump(a.index("Deploy"))
		return a, tea.Batch(cmd, func() tea.Msg { return startDeployMsg{m.opts} })
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
			if ms, ok := a.cur().(*manageScreen); ok && ms.inSubview() {
				break
			}
			if eh, ok := a.cur().(escHandler); ok && eh.handlesEsc() {
				break
			}
			return a, a.back()
		case "ctrl+n", "pgdown":
			if a.step >= 1 && a.step < a.index("Review") {
				return a, a.go_(1)
			}
		case "ctrl+p", "pgup":
			if !(deploying && ds.running) {
				return a, a.back()
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

// back leaves the current screen: Deploy returns to where it was started
// from and Manage to Welcome.
func (a *App) back() tea.Cmd {
	switch a.cur().name() {
	case "Deploy":
		return a.jump(a.deployBack)
	case "Manage":
		return a.jump(0)
	}
	if _, ok := a.cur().(toolScreen); ok {
		return a.jump(a.index("Manage"))
	}
	if a.cur().name() == "Tuning" && a.tuningFromManage {
		return a.jump(a.index("Manage"))
	}
	return a.go_(-1)
}

func (a *App) View() string {
	if a.w == 0 {
		return ""
	}
	w := a.w
	header := a.header(w)
	stepper := a.stepper(w)
	footer := a.footer(w)

	// The body panel's border takes 2 rows; its padding takes padY rows on
	// each side, and the title plus a blank line take two more.
	bodyH := a.h - lipgloss.Height(header) - lipgloss.Height(stepper) - lipgloss.Height(footer) - 2
	padY, padX := 1, 3
	if bodyH < 24 {
		padY = 0
	}
	if w < 80 {
		padX = 1
	}
	inner := w - 2 - 2*padX
	title := sTitle.Render(a.cur().title())
	contentH := max(3, bodyH-2*padY-2)
	content := a.cur().view(inner, contentH)
	if lines := strings.Split(content, "\n"); len(lines) > contentH {
		content = strings.Join(lines[:contentH], "\n") // never push the header off screen
	}
	body := sPanelFocus.Padding(padY, padX).Width(w - 2).Height(max(3, bodyH)).Render(title + "\n\n" + content)

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
	server := ""
	if p := config.Active(); p != config.DefaultProfile {
		server = sFaint.Render("  ·  server " + p)
	}
	domain := a.cfg.Domain
	if domain == "" {
		domain = "(no domain yet)"
	}
	if _, ok := a.cur().(toolScreen); ok {
		return " " + sAccent.Render("◆ Manage") + sMuted.Render("  "+domain) + sFaint.Render("  ›  ") + sTitle.Render(a.cur().name()) + server
	}
	_, fromTool := a.screens[a.deployBack].(toolScreen)
	if a.cur().name() == "Manage" || (a.cur().name() == "Deploy" && (a.deployBack == a.index("Manage") || fromTool)) {
		return " " + sAccent.Render("◆ Manage") + sMuted.Render("  "+domain) + server
	}
	var parts, dots []string
	for i, s := range a.wizard() {
		idx := i + 1
		switch {
		case idx == a.step:
			parts = append(parts, sAccent.Render("◉ "+s.name()))
			dots = append(dots, sAccent.Render("◉"))
		case idx < a.step:
			parts = append(parts, sGood.Render("●")+sMuted.Render(" "+s.name()))
			dots = append(dots, sGood.Render("●"))
		default:
			parts = append(parts, sFaint.Render("○ "+s.name()))
			dots = append(dots, sFaint.Render("○"))
		}
	}
	line := " " + strings.Join(parts, sFaint.Render("  ─  "))
	if lipgloss.Width(line) > w {
		// Too narrow: show dots plus the current step's name.
		line = " " + strings.Join(dots, "  ")
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
	line := " " + strings.Join(parts, sFaint.Render("   ·   "))
	if lipgloss.Width(line) > w {
		line = " " + strings.Join(parts, sFaint.Render(" · "))
	}
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

// switchServerMsg makes another saved server the active one. With fresh
// set, it is a new server and the wizard starts.
type switchServerMsg struct {
	name  string
	cfg   *config.Config
	fresh bool
}

func (a *App) switchServer(m switchServerMsg) tea.Cmd {
	a.cur().leave(a.cfg)
	if err := a.cfg.Save(); err != nil {
		a.status = sBad.Render("couldn't save config: " + err.Error())
		return nil
	}
	if err := config.SetActive(m.name); err != nil {
		a.status = sBad.Render("couldn't switch: " + err.Error())
		return nil
	}
	*a.cfg = *m.cfg // every screen holds this pointer
	if err := a.cfg.Save(); err != nil {
		a.status = sBad.Render("couldn't save config: " + err.Error())
	}
	a.status = sGood.Render("✓ now working on " + m.name)
	target := 0
	if m.fresh {
		target = a.index("Network")
	}
	a.step = target
	return a.cur().enter(a.cfg)
}
