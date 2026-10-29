// Package ui is the bubbletea front-end: a step-by-step wizard that ends in
// a live deployment view.
package ui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"interzone/internal/config"
	"interzone/internal/deploy"
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

// pager screens have more than one page in their step; nextPage moves on
// within the screen and reports false on its last page.
type pager interface{ nextPage() (tea.Cmd, bool) }

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

	deployBack  int // where esc leads from the Deploy screen
	mirrorsBack int // and from Mirrors

	sideCollapsed bool                 // the left menu is folded to its icon rail
	sideOpen      map[sideSection]bool // which of its sections are unfolded
	layout        string               // auto, compact or wide (layout.go)
	menuOpen      bool                 // the compact layout shows the menu
	sheetOpen     bool                 // the compact layout lists every key (⋯)
	gotoOpen      bool                 // ctrl+k's go-to box is up (goto.go)
	gotoIn        textinput.Model
	gotoSel       int
	menuTop       int    // how far the compact menu is scrolled
	sideFocus     bool   // the wide layout's keys are on the sidebar (menukeys.go)
	menuSel       string // the menu entry selected from the keyboard
	menuKeys      bool   // the selection is shown: the menu was reached by key
	menuCols      int    // tiles across in the compact menu, as last drawn
	menuFollow    bool   // scroll the compact menu to the selection
	menuFocusLine int    // the selection's line in the compact menu, or -1

	health    *deploy.Health // the header's dot (health.go)
	healthGen int
}

func New(cfg *config.Config, loaded bool) *App {
	zone.NewGlobal()
	loadTheme()
	deploy.Backfill(cfg)
	a := &App{cfg: cfg, loaded: loaded, sideCollapsed: loadSidebarCollapsed(), sideOpen: loadSideOpen(), layout: loadLayout()}
	a.screens = []screen{
		&welcomeScreen{loaded: loaded},
		newServerScreen(),
		&homeserverScreen{},
		newComponentsScreen(),
		&settingsScreen{},
		newReviewScreen(),
		newDeployScreen(),
		newManageScreen(), // not a wizard step: reached from Welcome
		// Reached from Manage:
		newTuningScreen(),
		newMaintenanceScreen(),
		newUpdatesScreen(),
		newHistoryScreen(),
		newRestoreScreen(),
		newBackupsScreen(),
		newAlertsScreen(),
		newUsersScreen(),
		newRoomsScreen(),
		newTokensScreen(),
		newDashboardScreen(),
		newServersScreen(),
		newMigrateScreen(),
		newMirrorsScreen(), // its own sidebar section
		newProbeScreen(),   // so is this
	}
	a.deployBack = a.index("Review")
	if cfg.Installed() {
		a.step = a.index("Manage") // esc still leads to Welcome
	}
	a.openSection(a.section())
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

func (a *App) Init() tea.Cmd { return tea.Batch(a.cur().enter(a.cfg), a.checkHealth()) }

func (a *App) cur() screen { return a.screens[a.step] }

func (a *App) go_(dir int) tea.Cmd {
	return a.jump(a.step + dir)
}

func (a *App) jump(n int) tea.Cmd {
	if n < 0 || n >= len(a.screens) || n == a.step {
		return nil
	}
	if a.screens[n].name() == "Mirrors" {
		a.mirrorsBack = a.step
	}
	if ds, ok := a.screens[n].(*deployScreen); ok {
		a.deployBack = a.step
		ds.fromManage = a.cur().name() == "Manage" || a.isTool(a.step)
	}
	a.cur().leave(a.cfg)
	if err := a.cfg.Save(); err != nil {
		a.status = "couldn't save config: " + err.Error()
	}
	was := a.section()
	a.step = n
	if sec := a.section(); sec != was {
		a.openSection(sec)
	}
	return a.cur().enter(a.cfg)
}

// ownedMsg is a result for one screen, named by owner.
type ownedMsg interface{ owner() string }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
	case tea.MouseMsg:
		return a.mouse(m)
	case ownedMsg:
		// A screen's result reaches it even when another one is showing by
		// now, or the screen would wait for it forever.
		for _, s := range a.screens {
			if s.name() == m.owner() {
				return a, s.update(msg)
			}
		}
	case navMsg:
		return a, a.go_(int(m))
	case formDoneMsg:
		if _, ok := a.cur().(*manageScreen); ok {
			return a, a.cur().update(m)
		}
		if _, ok := a.cur().(toolScreen); ok {
			return a, a.cur().update(m)
		}
		if ps, ok := a.cur().(presetSaving); ok && ps.savingPreset() {
			return a, a.cur().update(m)
		}
		return a, a.go_(1)
	case switchServerMsg:
		return a, tea.Batch(a.switchServer(m), a.restartHealth())
	case healthMsg, healthTickMsg:
		return a, a.onHealth(m)
	case gotoMsg:
		return a, a.jump(a.index(string(m)))
	case maintainMsg:
		a.screens[a.index("Maintenance")].(*maintenanceScreen).open(string(m))
		return a, a.jump(a.index("Maintenance"))
	case noteMsg:
		a.status = sGood.Render(string(m))
		return a, nil
	case autoBackMsg:
		if _, ok := a.cur().(*deployScreen); ok {
			a.status = sGood.Render("✓ " + m.text)
			return a, a.back()
		}
		return a, nil
	case runActionMsg:
		cmd := a.jump(a.index("Deploy"))
		return a, tea.Batch(cmd, func() tea.Msg { return startDeployMsg{m.opts} })
	case tea.KeyMsg:
		a.status = ""
		if a.gotoOpen {
			return a.gotoKey(m)
		}
		if m.String() == "ctrl+k" {
			a.openGoto()
			return a, nil
		}
		if a.sheetOpen {
			a.sheetOpen = false
			if m.String() == "esc" || m.String() == "q" {
				return a, nil
			}
		}
		if a.inMenu() {
			return a.menuKey(m)
		}
		ds, deploying := a.cur().(*deployScreen)
		switch m.String() {
		case "?":
			if !a.cur().typing() {
				a.sheetOpen = true
				return a, nil
			}
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
				if p, ok := a.cur().(pager); ok {
					if cmd, moved := p.nextPage(); moved {
						return a, cmd
					}
				}
				return a, a.go_(1)
			}
		case "ctrl+p", "pgup":
			if !(deploying && ds.running) {
				return a, a.back()
			}
		case "ctrl+b":
			if a.compact() {
				a.menuOpen, a.menuTop, a.menuKeys, a.menuFollow = true, 0, true, true
				a.selectHere()
			} else {
				a.focusSidebar()
			}
			return a, nil
		case "ctrl+l":
			a.cycleLayout()
			a.status = sMuted.Render("layout: ") + sTitle.Render(a.layoutName())
			return a, nil
		case "ctrl+y":
			cycleTheme()
			a.status = sMuted.Render("theme: ") + sTitle.Render(themeName)
			return a, nil
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
	case "Mirrors":
		return a.jump(a.mirrorsBack)
	case "Manage":
		return a.jump(0)
	}
	if _, ok := a.cur().(toolScreen); ok {
		return a.jump(a.index("Manage"))
	}
	return a.go_(-1)
}

func (a *App) View() (out string) {
	if plain {
		defer func() { out = plainText.Replace(out) }()
	}
	if a.w == 0 {
		return ""
	}
	marked = marked[:0]
	if a.compact() {
		return a.compactView()
	}
	a.menuOpen = false // the compact layout's; ? opens the sheet here too
	header := a.header(a.w)
	footer := a.footer(a.w)
	// With the menu open, it shows the steps; the stepper line is only
	// needed beside the folded rail.
	sideW := a.sidebarWidth()
	w := a.w - sideW
	stepper := ""
	if a.sidebarRail() {
		stepper = a.stepper(w)
	}

	// The body panel's border takes 2 rows; its padding takes padY rows on
	// each side, and the title plus a blank line take two more.
	stepH := 0
	if stepper != "" {
		stepH = lipgloss.Height(stepper)
	}
	bodyH := a.h - lipgloss.Height(header) - stepH - lipgloss.Height(footer) - 2
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
	var content string
	if a.gotoOpen {
		title, content = sTitle.Render("Go to")+sMuted.Render("  enter goes · esc closes"), a.gotoView(inner, contentH)
	} else if a.sheetOpen {
		title, content = sTitle.Render("Keys")+sMuted.Render("  any key closes this"), a.sheetView(inner, contentH)
	} else {
		content = a.screenView(inner, contentH)
	}
	if lines := strings.Split(content, "\n"); len(lines) > contentH {
		content = strings.Join(lines[:contentH], "\n") // never push the header off screen
	}
	panel := sPanelFocus // the purple border is where the keys go
	if a.sideFocus {
		panel = sPanel
	}
	body := panel.Padding(padY, padX).Width(w - 2).Height(max(3, bodyH)).Render(title + "\n\n" + content)
	body = lipgloss.JoinHorizontal(lipgloss.Top, a.sidebar(max(3, bodyH)), body)

	parts := []string{header}
	if stepper != "" {
		parts = append(parts, stepper)
	}
	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, append(parts, body, footer)...))
}

func (a *App) header(w int) string {
	brand := gradient("◢◤ interzone") + sMuted.Render("  matrix, deployed anywhere")
	mode := badge("ONLINE", cMint)
	if a.cfg.Sources.Blackout {
		mode = badge("BLACKOUT-READY", cAmber)
	}
	if hb := a.healthBadge(false); hb != "" {
		mode = hb + "   " + mode
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
	if a.cur().name() == "Mirrors" {
		return " " + sAccent.Render("⇅ Mirrors") + sMuted.Render("  what deploys download") + server
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

// layoutAware screens lay themselves out differently in the compact layout.
type layoutAware interface{ setCompact(bool) }

func (a *App) screenView(w, h int) string {
	if la, ok := a.cur().(layoutAware); ok {
		la.setCompact(a.compact())
	}
	return a.cur().view(w, h)
}

// escListed reports whether the screen's own keys say what esc does.
func (a *App) escListed() bool {
	for _, k := range a.cur().keys() {
		if k[0] == "esc" {
			return true
		}
	}
	return false
}

func (a *App) footer(w int) string {
	keys := slices.Clone(a.cur().keys())
	if a.sideFocus {
		keys = a.menuKeyHints()
	} else if a.step > 0 && !a.escListed() {
		keys = append(keys, [2]string{"esc", "back"})
	}
	if !a.cur().typing() {
		keys = append(keys, [2]string{"?", "all keys"}) // ? types itself in a form
	}
	keys = append(keys, [2]string{"ctrl+c", "quit"})
	join := func(sep string) string {
		var parts []string
		for _, k := range keys {
			parts = append(parts, hint(k[0], k[1]))
		}
		return " " + strings.Join(parts, sFaint.Render(sep))
	}
	line := join("   ·   ")
	if lipgloss.Width(line) > w {
		line = join(" · ")
	}
	// Still too wide: leave out the hints everyone knows, quit and then
	// the arrows, before the screen's own keys get cut off.
	for _, drop := range []string{"ctrl+c", "↑↓"} {
		if lipgloss.Width(line) <= w {
			break
		}
		keys = slices.DeleteFunc(keys, func(k [2]string) bool { return k[0] == drop })
		line = join(" · ")
	}
	// Still: the screen's last keys give way, before "? all keys".
	for lipgloss.Width(line) > w && len(keys) > 2 && keys[len(keys)-1][0] == "?" {
		keys = slices.Delete(keys, len(keys)-2, len(keys)-1)
		line = join(" · ")
	}
	if a.status != "" {
		line = " " + a.status
	}
	return truncateANSI(line, w)
}

// hint is a footer key hint; a one-key hint is clickable as a whole.
func hint(key, desc string) string {
	if _, ok := keyMsg(key); ok {
		return mark("key:"+key, sKey.Render(key)+" "+sMuted.Render(desc))
	}
	return keyLink(key) + " " + sMuted.Render(desc)
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
	note  string // the status line afterwards, if not the usual
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
	deploy.Backfill(a.cfg)
	if err := a.cfg.Save(); err != nil {
		a.status = sBad.Render("couldn't save config: " + err.Error())
	}
	a.status = sGood.Render("✓ now working on " + m.name)
	if m.note != "" {
		a.status = sGood.Render(m.note)
	}
	target := 0
	switch {
	case m.fresh:
		target = a.index("Server")
	case a.cfg.Installed():
		target = a.index("Manage")
	}
	a.step = target
	return a.cur().enter(a.cfg)
}
