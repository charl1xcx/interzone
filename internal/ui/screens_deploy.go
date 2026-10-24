package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Review ─────────────────────────────────────────────────────────

type startDeployMsg struct{ saveOnly bool }

type reviewScreen struct {
	cfg     *config.Config
	missing []string
	vp      viewport.Model
	yaml    string
}

func newReviewScreen() *reviewScreen { return &reviewScreen{vp: viewport.New(0, 0)} }

func (s *reviewScreen) name() string  { return "Review" }
func (s *reviewScreen) title() string { return "Everything look right?" }
func (s *reviewScreen) typing() bool  { return false }
func (s *reviewScreen) keys() [][2]string {
	k := [][2]string{{"↑↓", "scroll vars.yml"}, {"s", "save files only"}}
	if len(s.missing) == 0 {
		k = append([][2]string{{"enter", "deploy"}}, k...)
	}
	return k
}

func (s *reviewScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	c.FillGenerated()
	s.missing = c.Missing()
	cert, key := "<generated during deploy>", "<generated during deploy>"
	if c.SSL.Mode == "custom" {
		cert, key = c.SSL.CertPath, c.SSL.KeyPath
	}
	s.yaml = c.VarsYAML(config.MigrationVersion(c.PlaybookDir), cert, key)
	s.vp.SetContent(highlightYAML(s.yaml))
	s.vp.GotoTop()
	return nil
}

func (s *reviewScreen) leave(*config.Config) {}

func (s *reviewScreen) update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter", "d":
			if len(s.missing) == 0 {
				return tea.Sequence(nav(1), func() tea.Msg { return startDeployMsg{} })
			}
			return nil
		case "s":
			return tea.Sequence(nav(1), func() tea.Msg { return startDeployMsg{saveOnly: true} })
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return cmd
}

func (s *reviewScreen) view(w, h int) string {
	c := s.cfg
	hs := c.Homeserver
	for _, x := range catalog.Homeservers {
		if x.ID == c.Homeserver {
			hs = x.Name
		}
	}
	n := 0
	for _, comp := range catalog.Components {
		if c.Enabled(comp) {
			n++
		}
	}
	where := "this machine"
	if c.Target.Mode == "ssh" {
		where = c.Target.User + "@" + c.Target.Host
	}
	kv := func(k, v string) string {
		return lipgloss.NewStyle().Width(14).Render(sMuted.Render(k)) + sTitle.Render(v)
	}
	summary := []string{
		kv("Domain", c.Domain),
		kv("Homeserver", hs),
		kv("Install on", where),
		kv("Components", fmt.Sprint(n)),
		kv("Certificates", sslLabels[indexOf(sslModes, c.SSL.Mode)]),
		kv("Images from", map[bool]string{true: c.Sources.RegistryPrefix, false: "upstream registries"}[c.Sources.UseRegistry]),
	}

	var status []string
	if len(s.missing) > 0 {
		status = append(status, sBad.Render(fmt.Sprintf("✗ %d required setting(s) missing. Go back with esc:", len(s.missing))))
		for i, m := range s.missing {
			if i == 5 {
				status = append(status, sMuted.Render(fmt.Sprintf("  …and %d more", len(s.missing)-5)))
				break
			}
			status = append(status, sBad.Render("  • ")+m)
		}
	} else {
		status = append(status, sGood.Render("✓ Ready to deploy."))
	}
	status = append(status, "", sTitle.Render("DNS records pointing at your server:"))
	for _, r := range c.DNSRecords() {
		status = append(status, sPink.Render("  ◆ ")+r)
	}

	left := lipgloss.JoinVertical(lipgloss.Left, append(summary, append([]string{""}, status...)...)...)
	if w >= 110 {
		lw := 46
		s.vp.Width = w - lw - 4
		s.vp.Height = h - 2
		right := sPanel.Width(w - lw - 2).Render(sMuted.Render("vars.yml preview") + "\n" + s.vp.View())
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(lw).Render(left), right)
	}
	s.vp.Width = w - 4
	s.vp.Height = max(3, h-lipgloss.Height(left)-3)
	return left + "\n" + sPanel.Width(w-2).Render(s.vp.View())
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return 0
}

var yamlKey = regexp.MustCompile(`^(\s*-?\s*)([A-Za-z0-9_.]+)(:)(.*)$`)

func highlightYAML(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "# ──"):
			lines[i] = sPink.Render(l)
		case strings.HasPrefix(t, "#"):
			lines[i] = sMuted.Render(l)
		default:
			if m := yamlKey.FindStringSubmatch(l); m != nil {
				val := m[4]
				vs := lipgloss.NewStyle().Foreground(cText)
				if strings.Contains(m[2], "password") || strings.Contains(m[2], "secret") || strings.Contains(m[2], "token") || strings.Contains(m[2], "key") && len(strings.TrimSpace(val)) > 30 {
					val = " " + sFaint.Render("••••••••")
				} else if strings.TrimSpace(val) == "true" {
					vs = sGood
				} else if strings.TrimSpace(val) == "false" {
					vs = sBad
				}
				lines[i] = m[1] + sAccent.Render(m[2]) + sFaint.Render(m[3]) + vs.Render(val)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// ── Deploy ─────────────────────────────────────────────────────────

type deployEventMsg struct {
	ev deploy.Event
	ok bool
}

type stepState int

const (
	pending stepState = iota
	active
	done
	skipped
	failed
)

type deployScreen struct {
	cfg      *config.Config
	running  bool
	finished bool
	saveOnly bool
	err      error
	cancel   context.CancelFunc
	events   <-chan deploy.Event

	states  []stepState
	task    string
	tasks   int
	fails   int
	started time.Time
	elapsed time.Duration

	log     []string
	logFile *os.File
	vp      viewport.Model
	follow  bool
	spin    spinner.Model
	bar     progress.Model
}

func newDeployScreen() *deployScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Points
	sp.Style = sPink
	return &deployScreen{
		spin: sp,
		vp:   viewport.New(0, 0),
		bar:  progress.New(progress.WithGradient("#9D7CFF", "#3EF0B0"), progress.WithoutPercentage()),
	}
}

func (s *deployScreen) name() string { return "Deploy" }
func (s *deployScreen) title() string {
	switch {
	case s.running:
		return "Deploying…"
	case s.err != nil:
		return "Deployment stopped"
	case s.finished && s.saveOnly:
		return "Configuration saved"
	case s.finished:
		return "Your Matrix server is live ✨"
	}
	return "Deploy"
}
func (s *deployScreen) typing() bool { return false }
func (s *deployScreen) keys() [][2]string {
	if s.running {
		return [][2]string{{"↑↓", "scroll log"}, {"end", "follow"}, {"ctrl+c", "abort"}}
	}
	return [][2]string{{"r", "run again"}, {"↑↓", "scroll log"}, {"q", "quit"}}
}

func (s *deployScreen) enter(c *config.Config) tea.Cmd { s.cfg = c; return nil }
func (s *deployScreen) leave(*config.Config)           {}

func (s *deployScreen) abort() {
	if s.cancel != nil {
		s.cancel()
	}
	s.appendLog(sWarn.Render("aborting… (waiting for the current command to stop)"))
}

func (s *deployScreen) start(saveOnly bool) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.saveOnly = saveOnly
	s.running, s.finished, s.err = true, false, nil
	s.states = make([]stepState, len(deploy.StepNames))
	s.task, s.tasks, s.fails = "", 0, 0
	s.log = nil
	s.follow = true
	s.started = time.Now()
	if s.logFile != nil {
		s.logFile.Close()
	}
	_ = os.MkdirAll(config.Dir(), 0o700)
	s.logFile, _ = os.OpenFile(s.logPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	s.events = deploy.Start(ctx, s.cfg, deploy.Options{SaveOnly: saveOnly})
	return tea.Batch(s.spin.Tick, s.wait(), tick())
}

func (s *deployScreen) logPath() string { return filepath.Join(config.Dir(), "last-deploy.log") }

func (s *deployScreen) wait() tea.Cmd {
	ch := s.events
	return func() tea.Msg {
		ev, ok := <-ch
		return deployEventMsg{ev, ok}
	}
}

type tickMsg struct{}

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (s *deployScreen) appendLog(line string) {
	s.log = append(s.log, line)
	if len(s.log) > 5000 {
		s.log = s.log[len(s.log)-4000:]
	}
}

func (s *deployScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case startDeployMsg:
		return s.start(m.saveOnly)
	case tickMsg:
		if s.running {
			s.elapsed = time.Since(s.started)
			return tick()
		}
	case spinner.TickMsg:
		if s.running {
			var cmd tea.Cmd
			s.spin, cmd = s.spin.Update(m)
			return cmd
		}
	case deployEventMsg:
		if !m.ok {
			s.running = false
			return nil
		}
		s.handle(m.ev)
		return s.wait()
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			if !s.running {
				return s.start(s.saveOnly)
			}
		case "end", "G":
			s.follow = true
			s.vp.GotoBottom()
			return nil
		case "up", "k", "pgup", "home":
			s.follow = false
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return cmd
}

func (s *deployScreen) handle(ev deploy.Event) {
	if ev.Line != "" && s.logFile != nil {
		fmt.Fprintln(s.logFile, ev.Line)
	}
	switch {
	case ev.Err != nil:
		s.err = ev.Err
		s.running = false
		for i, st := range s.states {
			if st == active {
				s.states[i] = failed
			}
		}
		s.appendLog(sBad.Render("✗ " + ev.Err.Error()))
		return
	case ev.Done:
		s.finished = true
		s.running = false
		s.elapsed = time.Since(s.started)
		for i, st := range s.states {
			if st == active {
				s.states[i] = done
			}
		}
		return
	case ev.Started:
		for i := range s.states {
			if s.states[i] == active {
				s.states[i] = done
			}
		}
		s.states[ev.Step] = active
		return
	case ev.Skipped:
		s.states[ev.Step] = skipped
		return
	}
	if ev.Task != "" {
		s.task = ev.Task
		if strings.HasPrefix(ev.Line, "TASK") {
			s.tasks++
		}
	}
	if ev.Failed {
		s.fails++
	}
	s.appendLog(styleLogLine(ev.Line, ev.Failed))
}

func styleLogLine(l string, failed bool) string {
	switch {
	case failed:
		return sBad.Render(l)
	case strings.HasPrefix(l, "TASK"), strings.HasPrefix(l, "PLAY"), strings.HasPrefix(l, "RUNNING HANDLER"):
		return sAccent.Render(l)
	case strings.HasPrefix(l, "ok:"):
		return sGood.Render(l)
	case strings.HasPrefix(l, "changed:"):
		return sWarn.Render(l)
	case strings.HasPrefix(l, "skipping:"), strings.HasPrefix(l, "included:"):
		return sFaint.Render(l)
	case strings.HasPrefix(l, "$ "):
		return sPink.Render(l)
	case strings.Contains(l, "...ignoring"):
		return sMuted.Render(l)
	}
	return sMuted.Render(l)
}

func (s *deployScreen) view(w, h int) string {
	if s.states == nil {
		return sMuted.Render("Waiting to start…")
	}
	var steps []string
	for i, name := range deploy.StepNames {
		var icon string
		style := sMuted
		switch s.states[i] {
		case pending:
			icon = sFaint.Render("○")
			style = sFaint
		case active:
			icon = s.spin.View()
			style = sTitle
		case done:
			icon = sGood.Render("✓")
		case skipped:
			icon = sFaint.Render("–")
			style = sFaint
		case failed:
			icon = sBad.Render("✗")
			style = sBad
		}
		steps = append(steps, icon+" "+style.Render(name))
	}
	stepsCol := lipgloss.NewStyle().Width(32).Render(strings.Join(steps, "\n"))

	nDone := 0
	for _, st := range s.states {
		if st == done || st == skipped {
			nDone++
		}
	}
	s.bar.Width = max(10, w-36)
	stats := []string{
		s.bar.ViewAs(float64(nDone) / float64(len(s.states))),
		"",
		sMuted.Render("elapsed  ") + sTitle.Render(s.elapsed.Round(time.Second).String()),
		sMuted.Render("tasks    ") + sTitle.Render(fmt.Sprint(s.tasks)),
	}
	if s.fails > 0 {
		stats = append(stats, sMuted.Render("errors   ")+sBad.Render(fmt.Sprint(s.fails)))
	}
	if s.task != "" && s.running {
		stats = append(stats, "", sPink.Render("▸ ")+truncate(s.task, w-40))
	}
	top := lipgloss.JoinHorizontal(lipgloss.Top, stepsCol, "  ", strings.Join(stats, "\n"))

	var result string
	if s.finished || s.err != nil {
		result = s.resultView(w)
	}

	logH := max(3, h-lipgloss.Height(top)-lipgloss.Height(result)-3)
	s.vp.Width = w - 4
	s.vp.Height = logH
	lines := make([]string, len(s.log))
	for i, l := range s.log {
		lines[i] = truncateANSI(l, w-4)
	}
	s.vp.SetContent(strings.Join(lines, "\n"))
	if s.follow {
		s.vp.GotoBottom()
	}
	logBox := sPanel.Width(w - 2).Render(s.vp.View())

	parts := []string{top}
	if result != "" {
		parts = append(parts, result)
	}
	parts = append(parts, logBox)
	return strings.Join(parts, "\n")
}

func (s *deployScreen) resultView(w int) string {
	c := s.cfg
	if s.err != nil {
		body := sBad.Render("✗ "+s.err.Error()) + "\n" +
			sMuted.Render("Full log: ") + s.logPath() + "\n" +
			sMuted.Render("Fix the problem (esc goes back to the settings) and press ") + sKey.Render("r") + sMuted.Render(" to try again.")
		return sPanel.BorderForeground(cRed).Width(w - 2).Render(body)
	}
	inv := filepath.Join(c.PlaybookDir, "inventory")
	if s.saveOnly {
		body := sGood.Render("✓ Files written to ") + inv + "\n" +
			sMuted.Render("Deploy later with: ") + sTitle.Render("cd "+c.PlaybookDir+" && ansible-playbook -i inventory/hosts setup.yml --tags=install-all,ensure-matrix-users-created,start")
		return sPanel.BorderForeground(cMint).Width(w - 2).Render(body)
	}
	scheme := "https://"
	if c.SSL.Mode == "none" {
		scheme = "http://"
	}
	lines := []string{sGood.Render("✓ Done in " + s.elapsed.Round(time.Second).String())}
	lines = append(lines, sMuted.Render("Homeserver  ")+sTitle.Render(scheme+"matrix."+c.Domain))
	if c.Components["element"] {
		lines = append(lines, sMuted.Render("Element     ")+sTitle.Render(scheme+"element."+c.Domain))
	}
	switch c.Homeserver {
	case "synapse", "dendrite":
		lines = append(lines, sMuted.Render("Admin       ")+sTitle.Render("@"+c.AdminUser+":"+c.Domain)+sMuted.Render("  (password saved in "+config.Dir()+")"))
	case "continuwuity", "tuwunel":
		lines = append(lines, sMuted.Render("Register your first account with token ")+sTitle.Render(c.AdminPassword))
	case "conduit":
		lines = append(lines, sWarn.Render("Registration is open: create your account now, then disable it."))
	}
	if c.SSL.Mode == "selfsigned" {
		lines = append(lines, sWarn.Render("Self-signed certificate: import "+filepath.Join(config.Dir(), "ssl", c.Domain, "cert.pem")+" on your devices."))
	}
	return sPanel.BorderForeground(cMint).Width(w - 2).Render(strings.Join(lines, "\n"))
}
