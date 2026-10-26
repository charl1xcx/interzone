package ui

import (
	"context"
	"encoding/json"
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

type startDeployMsg struct{ opts deploy.Options }

type serverChecksMsg []deploy.Finding

type reviewScreen struct {
	cfg      *config.Config
	missing  []string
	vp       viewport.Model
	yaml     string
	checks   []deploy.Finding
	checking bool
	verbose  bool // list passed checks too
	confirm  bool // enter was pressed once despite failed checks
	spin     spinner.Model

	last     *deploy.Deploy // the last successful deploy, if any
	diff     []diffLine     // vars.yml then → now
	imported bool           // no deploy yet, but the server was imported:
	changes  []config.VarChange
	defaults map[string]string // the playbook's, to tell which additions change nothing
	showDiff bool
}

// comparable reports whether there's an earlier vars.yml to compare with.
func (s *reviewScreen) comparable() bool { return s.last != nil || s.imported }

func newReviewScreen() *reviewScreen {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	return &reviewScreen{vp: viewport.New(0, 0), spin: sp}
}

func (s *reviewScreen) name() string  { return "Review" }
func (s *reviewScreen) title() string { return "Everything look right?" }
func (s *reviewScreen) typing() bool  { return false }
func (s *reviewScreen) keys() [][2]string {
	k := [][2]string{{"c", "check again"}, {"v", "details"}, {"↑↓", "scroll vars.yml"}, {"s", "save files only"}}
	if s.comparable() {
		k = append(k, [2]string{"x", map[bool]string{true: "whole vars.yml", false: "what changes"}[s.showDiff]})
	}
	if len(s.missing) == 0 {
		k = append([][2]string{{"enter", "deploy"}}, k...)
	}
	return k
}

func (s *reviewScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	c.FillGenerated()
	s.missing = c.Missing()
	s.confirm = false
	cert, key := "<generated during deploy>", "<generated during deploy>"
	if c.SSL.Mode == "custom" {
		cert, key = c.SSL.CertPath, c.SSL.KeyPath
	}
	mv := config.MigrationVersion(c.PlaybookDir)
	s.yaml = c.VarsYAML(mv, cert, key)
	s.last, s.diff, s.imported, s.changes = nil, nil, false, nil
	if h := deploy.History(); len(h) > 0 {
		if prev, err := config.LoadFile(filepath.Join(h[0].Dir, "config.json")); err == nil {
			s.last = &h[0]
			s.diff = lineDiff(strings.Split(prev.VarsYAML(mv, cert, key), "\n"), strings.Split(s.yaml, "\n"))
		}
	} else if orig, ok := config.ImportedVars(config.Active()); ok {
		s.imported = true
		s.changes = config.VarChanges(orig, s.yaml)
		s.defaults = config.PlaybookDefaults(c.PlaybookDir)
	}
	s.showDiff = s.comparable()
	s.setPreview()
	return s.runChecks()
}

func (s *reviewScreen) setPreview() {
	if s.showDiff && s.imported {
		if len(s.changes) == 0 {
			s.vp.SetContent(sMuted.Render("Every variable keeps the value it had in the imported vars.yml."))
		} else {
			s.vp.SetContent(changesView(s.changes, s.defaults))
		}
	} else if s.showDiff {
		if a, r := diffChanges(s.diff); a+r == 0 {
			s.vp.SetContent(sMuted.Render("Nothing in vars.yml changes. An update still pulls the newest playbook and images."))
		} else {
			s.vp.SetContent(diffView(s.diff, 2))
		}
	} else {
		s.vp.SetContent(highlightYAML(s.yaml))
	}
	s.vp.GotoTop()
}

// changesView lists variables that differ from the imported vars.yml.
// An added variable at the playbook's default changes nothing, and says so.
func changesView(cs []config.VarChange, defaults map[string]string) string {
	var out []string
	for _, c := range cs {
		was, now := c.Old, c.New
		if secretVar(c.Name, was) || secretVar(c.Name, now) {
			if was != "" {
				was = "••••••••"
			}
			if now != "" {
				now = "••••••••"
			}
		}
		switch {
		case was == "" && config.SameYAML(defaults[c.Name], c.New):
			out = append(out, sFaint.Render("+ "+c.Name+": "+now), sFaint.Render("    the playbook's default, changes nothing"))
		case was == "":
			out = append(out, sGood.Render("+ "+c.Name+": "+now))
		case now == "":
			out = append(out, sBad.Render("- "+c.Name+": "+was))
		default:
			out = append(out, sWarn.Render("~ "+c.Name), sFaint.Render("    was ")+was, sFaint.Render("    now ")+now)
		}
	}
	return strings.Join(out, "\n")
}

// previewTitle heads the vars.yml panel.
func (s *reviewScreen) previewTitle() string {
	if !s.showDiff {
		return sMuted.Render("vars.yml preview")
	}
	if s.imported {
		return sMuted.Render("vars.yml compared with the imported one  ") + sWarn.Render(fmt.Sprintf("%d changed", len(s.changes)))
	}
	a, r := diffChanges(s.diff)
	return sMuted.Render("vars.yml since the deploy of "+s.last.Time.Local().Format("Jan 2 15:04")+"  ") +
		sGood.Render(fmt.Sprintf("+%d", a)) + " " + sBad.Render(fmt.Sprintf("-%d", r))
}

func (s *reviewScreen) runChecks() tea.Cmd {
	if s.checking || s.cfg.Domain == "" || (s.cfg.Target.Mode == "ssh" && s.cfg.Target.Host == "") {
		return nil
	}
	s.checking = true
	cfg := *s.cfg
	return tea.Batch(s.spin.Tick, func() tea.Msg { return serverChecksMsg(deploy.ServerChecks(&cfg)) })
}

func (s *reviewScreen) leave(*config.Config) {}

func (s *reviewScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case serverChecksMsg:
		s.checking = false
		s.checks = m
		return nil
	case spinner.TickMsg:
		if s.checking {
			var cmd tea.Cmd
			s.spin, cmd = s.spin.Update(m)
			return cmd
		}
		return nil
	case tea.KeyMsg:
		key := m.String()
		if key != "enter" && key != "d" {
			s.confirm = false
		}
		switch key {
		case "enter", "d":
			if len(s.missing) > 0 {
				return nil
			}
			if deploy.Worst(s.checks) == deploy.Fail && !s.confirm {
				s.confirm = true
				return nil
			}
			return tea.Sequence(nav(1), func() tea.Msg { return startDeployMsg{deploy.Options{Action: deploy.ActionInstall}} })
		case "s":
			return tea.Sequence(nav(1), func() tea.Msg { return startDeployMsg{deploy.Options{Action: deploy.ActionSaveOnly}} })
		case "c":
			return s.runChecks()
		case "v":
			s.verbose = !s.verbose
			return nil
		case "x":
			if s.last != nil {
				s.showDiff = !s.showDiff
				s.setPreview()
			}
			return nil
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
	kv := func(icon, k, v string) string {
		return sPink.Render(icon) + "  " + lipgloss.NewStyle().Width(14).Render(sMuted.Render(k)) + sTitle.Render(v)
	}
	summary := []string{
		kv("◎", "Domain", c.Domain),
		kv("◆", "Homeserver", hs),
		kv("⌂", "Install on", where),
		kv("▦", "Components", fmt.Sprint(n)),
		kv("◈", "Certificates", sslLabels[indexOf(sslModes, c.SSL.Mode)]),
		kv("⇩", "Images from", map[bool]string{true: c.Sources.RegistryPrefix, false: "upstream registries"}[c.Sources.UseRegistry]),
	}

	wide := w >= 110
	lw := w
	if wide {
		lw = w * 3 / 5
	}

	gap := []string{""}
	cw := min(lw, 72) // the cards' width
	var status []string
	tone := lipgloss.TerminalColor(cMint)
	if len(s.missing) > 0 {
		tone = cRed
		status = append(status, sBad.Render(fmt.Sprintf("✗ %d required setting(s) missing. Go back with esc:", len(s.missing))))
		for i, m := range s.missing {
			if i == 5 {
				status = append(status, sMuted.Render(fmt.Sprintf("  …and %d more", len(s.missing)-5)))
				break
			}
			status = append(status, sBad.Render("  • ")+m)
		}
	} else if s.confirm {
		tone = cAmber
		status = append(status, sWarn.Render("⚠ Some checks failed. Press enter again to deploy anyway."))
	} else if deploy.Worst(s.checks) == deploy.Fail {
		tone = cAmber
		status = append(status, sWarn.Render("! Settings are complete, but some checks below failed."))
	} else {
		status = append(status, sGood.Render("✓ Ready to deploy."))
	}
	for i, l := range status { // wrapped, as the card cuts long lines
		status[i] = lipgloss.NewStyle().Width(cw - 4).Render(l)
	}
	status = []string{card("", strings.Split(strings.Join(status, "\n"), "\n"), cw, 0, tone)}
	status = append(status, gap...)
	status = append(status, s.checksView(lw-2)...)
	summary = []string{card("", summary, cw, 0, cFaint)}

	left := lipgloss.JoinVertical(lipgloss.Left, append(summary, append(gap, status...)...)...)
	if wide {
		s.vp.Width = w - lw - 4
		s.vp.Height = h - 4
		right := sPanel.Width(w - lw - 2).Render(s.previewTitle() + "\n\n" + s.vp.View())
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(lw).Render(left), right)
	}
	s.vp.Width = w - 4
	s.vp.Height = h - lipgloss.Height(left) - 5
	if s.vp.Height < 3 {
		return left
	}
	return left + "\n\n" + sPanel.Width(w-2).Render(s.previewTitle()+"\n"+s.vp.View())
}

// checksView lists the pre-deploy checks. Passed server checks collapse
// into one line unless details are on; problems are always spelled out.
func (s *reviewScreen) checksView(w int) []string {
	head := rule("Pre-deploy checks", min(w, 72))
	if s.checking {
		sep := "  "
		if w < 60 {
			sep = "\n"
		}
		head = sSection.Render("Pre-deploy checks") + sep + s.spin.View() + sMuted.Render(" checking the server and DNS…")
	}
	lines := []string{head, ""}
	on := s.cfg.HostServices()
	svcW := 0
	for _, svc := range on {
		svcW = max(svcW, lipgloss.Width(svc)+2)
	}
	svcW = min(svcW, max(12, w/4))
	service := func(host string) string {
		if w < 60 {
			return sMuted.Render(on[host]) + "\n     " // its own line on a phone
		}
		return lipgloss.NewStyle().Width(svcW).Render(sMuted.Render(truncate(on[host], svcW-2)))
	}
	if s.checks == nil {
		for _, r := range s.cfg.DNSRecords() {
			lines = append(lines, sFaint.Render("  ○  ")+service(r)+r)
		}
		return lines
	}
	nameW, dnsW := 18, 18
	for _, f := range s.checks {
		if f.Group == "DNS" {
			dnsW = max(dnsW, min(34, len(f.Name)+2))
		}
	}
	for _, g := range []string{"Server", "DNS"} {
		passed := 0
		var rows []string
		for _, f := range s.checks {
			if f.Group != g {
				continue
			}
			if f.Level == deploy.Pass && !s.verbose && g == "Server" {
				passed++
				continue
			}
			icon, style := sGood.Render("✓"), sMuted
			switch f.Level {
			case deploy.Warn:
				icon, style = sWarn.Render("!"), sWarn
			case deploy.Fail:
				icon, style = sBad.Render("✗"), sBad
			}
			name := lipgloss.NewStyle().Width(nameW).Render(sTitle.Render(truncate(f.Name, nameW-2)))
			if g == "DNS" {
				name = lipgloss.NewStyle().Width(dnsW).Render(sTitle.Render(truncate(f.Name, dnsW-2)))
			}
			if w < 60 { // the detail goes under the name
				name = sTitle.Render(f.Name)
				if g == "DNS" {
					name = service(f.Name) + name
				}
				detail := style.Width(max(16, w-5)).Render(f.Detail)
				rows = append(rows, "  "+icon+"  "+name+"\n     "+strings.ReplaceAll(detail, "\n", "\n     "))
				continue
			}
			if g == "DNS" {
				name = service(f.Name) + name
			}
			pad := 5 + lipgloss.Width(name)
			detail := style.Width(max(16, w-pad)).Render(f.Detail)
			if f.Level == deploy.Pass {
				detail = style.Render(truncate(f.Detail, max(8, w-pad)))
			}
			detail = strings.ReplaceAll(detail, "\n", "\n"+strings.Repeat(" ", pad))
			rows = append(rows, "  "+icon+"  "+name+detail)
		}
		if passed > 0 {
			rows = append([]string{"  " + sGood.Render("✓") + "  " + sMuted.Render(fmt.Sprintf("%d server checks passed", passed)) + sFaint.Render("  (v for details)")}, rows...)
		}
		lines = append(lines, rows...)
	}
	return lines
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

// secretVar reports whether a vars.yml variable holds a secret, which is
// shown masked.
func secretVar(name, val string) bool {
	return strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "token") ||
		strings.Contains(name, "passphrase") || strings.Contains(name, "key") && len(strings.TrimSpace(val)) > 30
}

// maskLine hides the value of a "name: value" line that holds a secret.
func maskLine(l string) string {
	if m := yamlKey.FindStringSubmatch(l); m != nil && secretVar(m[2], m[4]) && strings.TrimSpace(m[4]) != "" {
		return m[1] + m[2] + m[3] + " ••••••••"
	}
	return l
}

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
				if secretVar(m[2], val) {
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
	warned
)

type deployScreen struct {
	cfg      *config.Config
	opts     deploy.Options
	running  bool
	finished bool
	err      error
	result   *deploy.Result
	cancel   context.CancelFunc
	events   <-chan deploy.Event

	plan    []deploy.Step
	states  map[deploy.Step]stepState
	task    string
	tasks   int
	fails   int
	lastErr string // message of the most recent failed task
	started time.Time
	elapsed time.Duration

	fromManage bool      // started from Manage or one of its tools
	backAt     time.Time // when a finished quick action returns to Manage; zero: stay

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

var actionTitles = map[deploy.Action][3]string{ // running, done, failed
	deploy.ActionInstall:      {"Deploying…", "Your Matrix server is live ✨", "Deployment stopped"},
	deploy.ActionSaveOnly:     {"Writing configuration…", "Configuration saved", "Couldn't write the configuration"},
	deploy.ActionSelfCheck:    {"Checking services…", "Self-check finished", "Self-check couldn't run"},
	deploy.ActionRestart:      {"Restarting services…", "Services restarted", "Restart failed"},
	deploy.ActionRegister:     {"Registering the user…", "User registered", "Couldn't register the user"},
	deploy.ActionBackup:       {"Backing up…", "Backup saved", "Backup failed"},
	deploy.ActionLogs:         {"Fetching logs…", "Logs", "Couldn't fetch logs"},
	deploy.ActionImages:       {"Checking container images…", "Image check finished", "Couldn't check the images"},
	deploy.ActionRollback:     {"Rolling back…", "Rolled back", "Rollback stopped"},
	deploy.ActionSelfUpdate:   {"Updating interzone…", "interzone updated", "Couldn't update interzone"},
	deploy.ActionRestore:      {"Restoring the database…", "Database restored", "Restore stopped"},
	deploy.ActionSchedule:     {"Setting up scheduled backups…", "Scheduled backups set", "Couldn't set up scheduled backups"},
	deploy.ActionFetchBackups: {"Downloading backups…", "Backups downloaded", "Couldn't download the backups"},
	deploy.ActionMigrate:      {"Moving the server…", "Moved to the new server ✨", "The move stopped"},
}

func (s *deployScreen) name() string { return "Deploy" }
func (s *deployScreen) title() string {
	t := actionTitles[s.opts.Action]
	switch {
	case s.running:
		return t[0]
	case s.err != nil:
		return t[2]
	case s.finished && s.result != nil && len(s.result.Problems) > 0 && s.opts.Action == deploy.ActionInstall:
		return "Installed, but some services aren't answering"
	case s.finished && s.result != nil && len(s.result.Problems) > 0:
		return "Self-check found problems"
	case s.finished:
		return t[1]
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

func (s *deployScreen) start(opts deploy.Options) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.opts = opts
	s.running, s.finished, s.err, s.result = true, false, nil, nil
	s.backAt = time.Time{}
	s.plan = deploy.Plan(opts.Action)
	s.states = map[deploy.Step]stepState{}
	s.task, s.tasks, s.fails, s.lastErr = "", 0, 0, ""
	s.log = nil
	s.follow = true
	s.started = time.Now()
	s.elapsed = 0
	if s.logFile != nil {
		s.logFile.Close()
	}
	_ = os.MkdirAll(config.Dir(), 0o700)
	s.logFile, _ = os.OpenFile(s.logPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	s.events = deploy.Start(ctx, s.cfg, opts)
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

// autoBackMsg asks the app to leave the Deploy screen after a quick action
// went fine; text is the toast to show.
type autoBackMsg struct{ text string }

// autoBackActions are the actions whose outcome a one-line toast tells in
// full, so their result screen needn't wait for esc. The rest show
// something to read (a password, logs, problems) or are rare and big.
var autoBackActions = map[deploy.Action]bool{
	deploy.ActionInstall: true, deploy.ActionSelfCheck: true, deploy.ActionRestart: true,
	deploy.ActionImages: true, deploy.ActionSchedule: true,
}

const autoBackDelay = 4 * time.Second

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
		return s.start(m.opts)
	case tickMsg:
		if s.running {
			s.elapsed = time.Since(s.started)
			return tick()
		}
		if !s.backAt.IsZero() {
			if time.Now().Before(s.backAt) {
				return tick()
			}
			s.backAt = time.Time{}
			text := actionTitles[s.opts.Action][1]
			return func() tea.Msg { return autoBackMsg{text} }
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
		s.backAt = time.Time{} // any key stays on the result
		switch m.String() {
		case "r":
			if !s.running && s.plan != nil {
				return s.start(s.opts)
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

func (s *deployScreen) finishActive(to stepState) {
	for st, v := range s.states {
		if v == active {
			s.states[st] = to
		}
	}
}

func (s *deployScreen) handle(ev deploy.Event) {
	if ev.Line != "" && s.logFile != nil {
		fmt.Fprintln(s.logFile, ev.Line)
	}
	switch {
	case ev.Plan != nil:
		s.plan = ev.Plan
		return
	case ev.Err != nil:
		s.err = ev.Err
		s.running = false
		s.finishActive(failed)
		s.appendLog(sBad.Render("✗ " + ev.Err.Error()))
		return
	case ev.Done:
		s.finished = true
		s.running = false
		s.result = ev.Result
		s.elapsed = time.Since(s.started)
		s.finishActive(done)
		if s.fromManage && autoBackActions[s.opts.Action] && (s.result == nil || len(s.result.Problems) == 0) && s.fails == 0 {
			s.backAt = time.Now().Add(autoBackDelay)
		}
		return
	case ev.Moved:
		// The data now lives on the new server: from here on it is the
		// server, even if a later step fails.
		s.cfg.Target = s.opts.NewTarget
		_ = s.cfg.Save()
		s.appendLog(sGood.Render("✓ /matrix is on the new server; it is now the saved target"))
		return
	case ev.Started:
		s.finishActive(done)
		s.states[ev.Step] = active
		return
	case ev.Warn:
		s.states[ev.Step] = warned
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
		s.lastErr = ev.Line
		if _, js, ok := strings.Cut(ev.Line, "=> "); ok {
			var v struct {
				Msg string `json:"msg"`
			}
			if json.Unmarshal([]byte(js), &v) == nil && v.Msg != "" {
				s.lastErr = v.Msg
			}
		}
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
	}
	return sMuted.Render(l)
}

func (s *deployScreen) view(w, h int) string {
	if s.plan == nil {
		return sMuted.Render("Waiting to start…")
	}
	var steps []string
	for _, st := range s.plan {
		var icon string
		style := sMuted
		switch s.states[st] {
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
		case warned:
			icon = sWarn.Render("!")
			style = sWarn
		}
		steps = append(steps, icon+"  "+style.Render(st.String()))
	}
	sep := "\n"
	if h >= 30 {
		sep = "\n\n" // room to spare: space the steps out
	}
	narrow := w < 72 // the stats go above the steps instead of beside them
	if narrow {
		sep = "\n"
	}
	stepsCol := lipgloss.NewStyle().Width(36).Render(strings.Join(steps, sep))

	nDone := 0
	for _, st := range s.plan {
		if v := s.states[st]; v == done || v == skipped || v == warned {
			nDone++
		}
	}
	s.bar.Width = max(10, w-40)
	if narrow {
		s.bar.Width = max(10, w-2)
	}
	stats := []string{
		s.bar.ViewAs(float64(nDone) / float64(len(s.plan))),
		"",
		sMuted.Render("elapsed  ") + sTitle.Render(s.elapsed.Round(time.Second).String()),
		sMuted.Render("tasks    ") + sTitle.Render(fmt.Sprint(s.tasks)),
	}
	if s.fails > 0 {
		stats = append(stats, sMuted.Render("errors   ")+sBad.Render(fmt.Sprint(s.fails)))
	}
	if s.task != "" && s.running {
		stats = append(stats, "", sPink.Render("▸ ")+truncate(s.task, w-44))
		if narrow {
			stats[len(stats)-1] = sPink.Render("▸ ") + truncate(s.task, w-4)
		}
	}
	top := lipgloss.JoinHorizontal(lipgloss.Top, stepsCol, "    ", strings.Join(stats, "\n"))
	if narrow {
		top = strings.Join(stats, "\n") + "\n\n" + stepsCol
	}

	var result string
	if s.finished || s.err != nil {
		result = s.resultView(w)
	}
	if !s.backAt.IsZero() {
		left := max(1, int(time.Until(s.backAt).Round(time.Second)/time.Second))
		result += "\n" + sMuted.Render(fmt.Sprintf("↩ back to Manage in %ds · any key stays here", left))
	}

	gaps := 1 // blank lines between the sections below
	if result != "" {
		gaps = 2
	}
	logH := max(3, h-lipgloss.Height(top)-lipgloss.Height(result)-gaps-3)
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
	return strings.Join(parts, "\n\n")
}

func (s *deployScreen) resultView(w int) string {
	c := s.cfg
	panel := func(color lipgloss.TerminalColor, lines ...string) string {
		return sPanel.BorderForeground(color).Width(w - 2).Render(strings.Join(lines, "\n"))
	}
	if s.err != nil {
		hint := "Fix the problem (esc goes back) and press "
		lines := []string{sBad.Render("✗ " + s.err.Error())}
		if s.lastErr != "" {
			lines = append(lines, sMuted.Render(truncate(s.lastErr, 3*(w-6))))
		}
		return panel(cRed, append(lines,
			sMuted.Render("Full log: ")+s.logPath(),
			sMuted.Render(hint)+keyLink("r")+sMuted.Render(" to try again."))...)
	}
	res := s.result
	if res == nil {
		res = &deploy.Result{}
	}
	switch s.opts.Action {
	case deploy.ActionSaveOnly:
		inv := filepath.Join(c.PlaybookDir, "inventory")
		return panel(cMint,
			sGood.Render("✓ Files written to ")+inv,
			sMuted.Render("Deploy later with: ")+sTitle.Render("cd "+c.PlaybookDir+" && ansible-playbook -i inventory/hosts setup.yml --tags=install-all,ensure-matrix-users-created,start"))
	case deploy.ActionSelfCheck:
		color := lipgloss.TerminalColor(cMint)
		if len(res.Problems) > 0 {
			color = cAmber
		}
		return panel(color, checkSummary(res))
	case deploy.ActionRestart:
		return panel(cMint, sGood.Render("✓ Every service was restarted in "+s.elapsed.Round(time.Second).String()+"."))
	case deploy.ActionRegister:
		return panel(cMint,
			sGood.Render("✓ Registered ")+sTitle.Render("@"+s.opts.User.Name+":"+c.Domain),
			sMuted.Render("Password  ")+sTitle.Render(s.opts.User.Password)+sMuted.Render("   (shown once; it isn't saved)"))
	case deploy.ActionBackup:
		return panel(cMint,
			sGood.Render("✓ Database dump saved to ")+sTitle.Render(res.File),
			sMuted.Render("Restore it from Manage › Restore a backup."))
	case deploy.ActionImages:
		if len(res.MissingImages) == 0 {
			return panel(cMint, sGood.Render(fmt.Sprintf("✓ All %d images can be pulled.", len(res.Images))))
		}
		return panel(cAmber, imageSummary(res, w)...)
	case deploy.ActionSelfUpdate:
		if res.File == "" {
			return panel(cMint, sGood.Render("✓ interzone "+res.Version+" is the newest release."))
		}
		return panel(cMint, sGood.Render("✓ Installed interzone ")+sTitle.Render(res.Version)+sGood.Render(" to ")+sTitle.Render(res.File),
			sMuted.Render("Quit (q) and start interzone again to use it."))
	case deploy.ActionRestore:
		return panel(cMint, sGood.Render("✓ The database of "+c.Domain+" was restored from ")+sTitle.Render(filepath.Base(s.opts.Backup)),
			sMuted.Render("The database as it was just before is in "+deploy.BackupDir()+"; every service was started again."))
	case deploy.ActionSchedule:
		if !c.Backups.Enabled {
			return panel(cMint, sGood.Render("✓ Scheduled backups are off."),
				sMuted.Render("Dumps already on the server stay in "+deploy.ServerBackupDir+"."))
		}
		return panel(cMint, sGood.Render("✓ The server backs up its database ")+sTitle.Render(c.Backups.When)+
			sGood.Render(fmt.Sprintf(", keeping %d.", c.Backups.Keep)),
			sMuted.Render("Dumps go to "+deploy.ServerBackupDir+"; download them from Manage › Restore with d."))
	case deploy.ActionFetchBackups:
		n := len(deploy.ListBackups(c.Domain))
		return panel(cMint, sGood.Render("✓ The server's backups are in ")+sTitle.Render(res.File),
			sMuted.Render(fmt.Sprintf("%d backup(s) of %s on this computer now.", n, c.Domain)))
	case deploy.ActionLogs:
		return ""
	}

	scheme := "https://"
	if c.SSL.Mode == "none" {
		scheme = "http://"
	}
	lines := []string{sGood.Render("✓ Done in " + s.elapsed.Round(time.Second).String())}
	switch s.opts.Action {
	case deploy.ActionRollback:
		if pin := c.PinnedCommit; pin != "" {
			lines = append(lines, sWarn.Render("The playbook is held at "+pin[:min(10, len(pin))]+"; Manage › Update the server releases it."))
		}
	case deploy.ActionMigrate:
		lines = append(lines,
			sWarn.Render("Point these DNS records at the new server now: ")+sTitle.Render(hostList(c)),
			sMuted.Render("The old server's Matrix services are stopped and disabled; its /matrix stays until you delete it."))
	}
	lines = append(lines, sMuted.Render("Homeserver  ")+sTitle.Render(scheme+"matrix."+c.Domain))
	if c.Components["element"] {
		lines = append(lines, sMuted.Render("Element     ")+sTitle.Render(scheme+"element."+c.Domain))
	}
	switch c.Homeserver {
	case "synapse", "dendrite":
		lines = append(lines, sMuted.Render("Admin       ")+sTitle.Render("@"+c.AdminUser+":"+c.Domain)+sMuted.Render("  (password saved in "+config.Dir()+")"))
		if c.RegistrationOpen() {
			lines = append(lines, sMuted.Render("Sign-up     ")+sTitle.Render(c.SignupToken)+sMuted.Render("  (share this token; close sign-up from Manage)"))
		}
	case "continuwuity", "tuwunel":
		if c.RegistrationOpen() {
			lines = append(lines, sMuted.Render("Register your first account with token ")+sTitle.Render(c.AdminPassword)+sMuted.Render(", then close registration from Manage."))
		}
	case "conduit":
		if c.RegistrationOpen() {
			lines = append(lines, sWarn.Render("Registration is open: create your account now, then close it from Manage."))
		}
	}
	if c.SSL.Mode == "selfsigned" {
		lines = append(lines, sWarn.Render("Self-signed certificate: import "+filepath.Join(config.Dir(), "ssl", c.Domain, "cert.pem")+" on your devices."))
	}
	if len(res.MissingImages) > 0 {
		lines = append(lines, "")
		lines = append(lines, imageSummary(res, w)...)
	}
	if len(res.Passed) > 0 || len(res.Problems) > 0 {
		lines = append(lines, "", checkSummary(res))
	}
	color := lipgloss.TerminalColor(cMint)
	if len(res.Problems) > 0 || len(res.MissingImages) > 0 {
		color = cAmber
	}
	return panel(color, lines...)
}

// checkSummary lists self-check results: what answered and what didn't.
func checkSummary(res *deploy.Result) string {
	var lines []string
	if len(res.Passed) > 0 {
		lines = append(lines, sGood.Render("✓ Working: ")+sMuted.Render(strings.Join(res.Passed, ", ")))
	}
	for _, p := range res.Problems {
		lines = append(lines, sBad.Render("✗ ")+p)
	}
	if len(res.Problems) > 0 {
		lines = append(lines, sFaint.Render("The self-check stops at the first failure; fix it and run it again from Manage."))
	} else if len(res.Passed) == 0 {
		lines = append(lines, sMuted.Render("Nothing to check for the enabled services."))
	}
	return strings.Join(lines, "\n")
}

// imageSummary lists the images the registries couldn't serve.
func imageSummary(res *deploy.Result, w int) []string {
	lines := []string{sWarn.Render(fmt.Sprintf("! %d of %d images can't be pulled:", len(res.MissingImages), len(res.MissingImages)+len(res.Images)))}
	for i, m := range res.MissingImages {
		if i == 8 {
			lines = append(lines, sFaint.Render(fmt.Sprintf("  …and %d more (see the log)", len(res.MissingImages)-i)))
			break
		}
		lines = append(lines, sMuted.Render("  "+truncate(m, w-8)))
	}
	return append(lines, sMuted.Render("Copy them into your registry mirror, or switch the container registry in the setup."))
}

// hostList is the DNS records with what each serves, for a sentence:
// "Synapse: matrix.example.org; Element Web: element.example.org".
func hostList(c *config.Config) string {
	on := c.HostServices()
	var parts []string
	for _, h := range c.DNSRecords() {
		if on[h] != "" {
			h = on[h] + ": " + h
		}
		parts = append(parts, h)
	}
	return strings.Join(parts, "; ")
}
