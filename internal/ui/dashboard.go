package ui

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Dashboard ──────────────────────────────────────────────────────
//
// Tiles for CPU, memory, disk and containers across the top, then the
// details in cards: the system and its resources, what uses the most,
// certificates and every container. The wide layout puts cards side by
// side; the compact one stacks them and keeps container rows short.

type statusMsg struct {
	server string
	st     *deploy.ServerStatus
	certs  bool // the certificates were checked too
	err    error
}

func (statusMsg) owner() string { return "Dashboard" }

// dashTickMsg is the time for the next refresh; gen tells an old one apart.
type dashTickMsg struct {
	server string
	gen    int
}

func (dashTickMsg) owner() string { return "Dashboard" }

// The dashboard refreshes itself this long after the last refresh ended,
// while it's on screen (longer after a failed one), and checks the
// certificates every certsEvery.
const (
	dashEvery  = 5 * time.Second
	dashRetry  = 30 * time.Second
	certsEvery = 5 * time.Minute
)

type dashboardScreen struct {
	cfg      *config.Config
	server   string // whose status is shown
	st       *deploy.ServerStatus
	err      error
	loading  bool
	spin     spinner.Model
	scroll   int
	narrow   bool // the compact layout is on
	shown    bool // on screen, so worth refreshing
	gen      int  // of the pending dashTickMsg
	certsAt  time.Time
	certsNow bool // the next refresh checks the certificates whatever their age
}

func newDashboardScreen() *dashboardScreen { return &dashboardScreen{spin: newSpinner()} }

func (s *dashboardScreen) tool()                {}
func (s *dashboardScreen) name() string         { return "Dashboard" }
func (s *dashboardScreen) title() string        { return "How " + s.cfg.Domain + " is doing" }
func (s *dashboardScreen) typing() bool         { return false }
func (s *dashboardScreen) leave(*config.Config) { s.shown = false }
func (s *dashboardScreen) setCompact(on bool)   { s.narrow = on }
func (s *dashboardScreen) keys() [][2]string {
	return [][2]string{{"r", "refresh"}, {"↑↓", "scroll"}}
}
func (s *dashboardScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.shown = c, true
	if srv := config.Active(); srv != s.server {
		s.server, s.st, s.err, s.loading, s.scroll, s.certsAt = srv, nil, nil, false, 0, time.Time{}
		if st, _, ok := loadCache[deploy.ServerStatus]("status"); ok {
			s.st = &st // shown while the fresh one loads
		}
	}
	return s.refresh()
}

func (s *dashboardScreen) refresh() tea.Cmd {
	if s.loading {
		return nil
	}
	s.loading = true
	cfg, srv := *s.cfg.Clone(), s.server
	certs := s.certsNow || s.st == nil || len(s.st.Certs) == 0 || time.Since(s.certsAt) > certsEvery
	s.certsNow = false
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		st, err := deploy.Status(ctx, &cfg, certs)
		return statusMsg{srv, st, certs, err}
	})
}

// next schedules the next refresh, while the dashboard is on screen.
func (s *dashboardScreen) next() tea.Cmd {
	if !s.shown {
		return nil
	}
	after := dashEvery
	if s.err != nil {
		after = dashRetry
	}
	s.gen++
	srv, gen := s.server, s.gen
	return tea.Tick(after, func(time.Time) tea.Msg { return dashTickMsg{srv, gen} })
}

func (s *dashboardScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case statusMsg:
		if m.server != s.server {
			return nil // from before switching servers
		}
		s.loading, s.err = false, m.err
		if m.err == nil && m.st != nil {
			if m.certs {
				s.certsAt = m.st.Taken
			} else if s.st != nil {
				m.st.Certs, m.st.History = s.st.Certs, s.st.History
			}
			s.st = m.st
			saveCache("status", *m.st)
		}
		return s.next()
	case dashTickMsg:
		if m.server == s.server && m.gen == s.gen && s.shown {
			return s.refresh()
		}
	case spinner.TickMsg:
		return spinning(s.loading, &s.spin, m)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			s.certsNow = true
			return s.refresh()
		case "up", "k":
			s.scroll = max(0, s.scroll-1)
		case "down", "j":
			s.scroll++
		case "home", "g":
			s.scroll = 0
		}
	}
	return nil
}

// meter draws a usage bar, amber above 75% and red above 90%.
func meter(frac float64, w int) string { return bar(frac, w, usageStyle(frac)) }

func bar(frac float64, w int, style lipgloss.Style) string {
	frac = max(0, min(1, frac))
	n := int(frac*float64(w) + 0.5)
	return style.Render(strings.Repeat("█", n)) + sFaint.Render(strings.Repeat("░", max(0, w-n)))
}

// usageColor is a tile's border: calm, amber above 75%, red above 90%.
func usageColor(frac float64) lipgloss.TerminalColor {
	switch {
	case frac > 0.9:
		return cRed
	case frac > 0.75:
		return cAmber
	}
	return cFaint
}

// card is a bordered box, w columns wide, headed by title.
func card(title string, lines []string, w, h int, border lipgloss.TerminalColor) string {
	inner := max(1, w-4)
	for i, l := range lines {
		lines[i] = truncateANSI(l, inner)
	}
	body := strings.Join(lines, "\n")
	if title != "" {
		body = sSection.Render(title) + "\n" + body
	}
	st := sPanel.BorderForeground(border).Width(w - 2)
	if h > 0 {
		st = st.Height(h)
	}
	return st.Render(body)
}

// columns puts cards side by side with a one-column gap, all as tall as
// the tallest.
func columns(w int, parts ...func(w, h int) string) string {
	n := len(parts)
	cw := (w - (n - 1)) / n
	h := 0
	for _, p := range parts {
		h = max(h, lipgloss.Height(p(cw, 0))-2)
	}
	var out []string
	for i, p := range parts {
		if i > 0 {
			out = append(out, " ")
		}
		pw := cw
		if i == n-1 {
			pw = w - (n-1)*(cw+1)
		}
		out = append(out, p(pw, h))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, out...)
}

func (s *dashboardScreen) view(w, h int) string {
	if s.st == nil {
		if s.err != nil {
			return errorView(s.err)
		}
		return s.spin.View() + sMuted.Render(" asking the server…")
	}
	st := s.st
	var blocks []string
	if s.err != nil {
		blocks = append(blocks, sBad.Render("✗ "+s.err.Error()))
	}
	// A clock time, as nothing redraws a "5s ago" between refreshes.
	dot := sGood.Render("●")
	if s.loading {
		dot = s.spin.View()
	}
	every := " live · every " + dashEvery.String()
	if s.err != nil {
		dot, every = sBad.Render("●"), " trying again every "+dashRetry.String()
	}
	when := dot + sFaint.Render(every+" · last at "+st.Taken.Format("15:04:05"))
	blocks = append(blocks, s.headline(w)+"\n"+when)

	wide := !s.narrow && w >= 80
	tiles := s.tiles()
	switch {
	case wide && w >= 100:
		blocks = append(blocks, columns(w, tiles...))
	case w >= 44:
		blocks = append(blocks, columns(w, tiles[0], tiles[1]), columns(w, tiles[2], tiles[3]))
	default:
		blocks = append(blocks, s.compactMeters(w))
	}

	if wide && w >= 110 {
		blocks = append(blocks, columns(w, s.systemCard, s.topCard))
	} else {
		blocks = append(blocks, s.systemCard(w, 0))
		if w >= 44 {
			blocks = append(blocks, s.topCard(w, 0))
		}
	}
	if len(st.History) >= 2 && w >= 44 {
		blocks = append(blocks, s.historyCard(w))
	}
	if len(st.Certs) > 0 {
		blocks = append(blocks, s.certCard(w))
	}
	blocks = append(blocks, s.containerCard(w, wide))

	lines := strings.Split(strings.Join(blocks, "\n"), "\n")
	s.scroll = min(s.scroll, max(0, len(lines)-h))
	end := min(len(lines), s.scroll+h)
	shown := lines[s.scroll:end]
	if end < len(lines) && len(shown) > 0 {
		shown[len(shown)-1] = sFaint.Render(fmt.Sprintf("  ↓ %d more lines · ↑↓ scroll", len(lines)-end))
	}
	return strings.Join(shown, "\n")
}

// headline is the server in one line: name, OS, kernel, Docker, uptime.
func (s *dashboardScreen) headline(w int) string {
	st := s.st
	dot := sGood.Render("●")
	for _, c := range st.Containers {
		if !c.Healthy() {
			dot = sWarn.Render("●")
		}
	}
	name := s.cfg.Domain
	if st.Hostname != "" && st.Hostname != name {
		name += sFaint.Render(" (" + st.Hostname + ")")
	}
	parts := []string{dot + " " + sTitle.Render(name)}
	if st.OS != "" {
		parts = append(parts, sMuted.Render(st.OS))
	}
	if st.Kernel != "" {
		parts = append(parts, sMuted.Render("kernel "+st.Kernel))
	}
	if st.Docker != "" {
		parts = append(parts, sMuted.Render("Docker "+st.Docker))
	}
	if st.Uptime > 0 {
		parts = append(parts, sMuted.Render("up "+roundDuration(st.Uptime)))
	}
	sep := sFaint.Render("  ·  ")
	line := strings.Join(parts, sep)
	if lipgloss.Width(line) <= w {
		return line
	}
	// Too long: wrap at the separators.
	var out []string
	cur := ""
	for _, p := range parts {
		switch {
		case cur == "":
			cur = p
		case lipgloss.Width(cur+sep+p) <= w:
			cur += sep + p
		default:
			out = append(out, cur)
			cur = "  " + p
		}
	}
	return strings.Join(append(out, cur), "\n")
}

func (s *dashboardScreen) cpuFrac() float64 {
	if s.st.CPUs == 0 {
		return 0
	}
	return s.st.Load[0] / float64(s.st.CPUs)
}

func (s *dashboardScreen) memFrac() float64 {
	if s.st.MemTotal == 0 {
		return 0
	}
	return float64(s.st.MemTotal-s.st.MemAvail) / float64(s.st.MemTotal)
}

// rootDisk is the fullest disk.
func (s *dashboardScreen) rootDisk() (deploy.Disk, float64) {
	var d deploy.Disk
	frac := -1.0
	for _, x := range s.st.Disks {
		if x.Total > 0 && float64(x.Used)/float64(x.Total) > frac {
			d, frac = x, float64(x.Used)/float64(x.Total)
		}
	}
	return d, max(0, frac)
}

func (s *dashboardScreen) counts() (running, stopped, unhealthy, restarted int) {
	for _, c := range s.st.Containers {
		switch {
		case c.State != "running":
			stopped++
		case c.Health == "unhealthy":
			unhealthy++
			running++
		default:
			running++
		}
		if c.Restarts > 0 {
			restarted++
		}
	}
	return
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

// tile is a stat box: a label, a big number with a note and a bar.
func tile(label, value, note string, frac float64, barStyle lipgloss.Style, border lipgloss.TerminalColor) func(w, h int) string {
	return func(w, h int) string {
		inner := max(4, w-4)
		return card("", []string{
			sMuted.Render(label),
			sTitle.Render(value) + "  " + sFaint.Render(truncate(note, max(1, inner-lipgloss.Width(value)-2))),
			bar(frac, inner, barStyle),
		}, w, h, border)
	}
}

func usageStyle(frac float64) lipgloss.Style {
	switch {
	case frac > 0.9:
		return sBad
	case frac > 0.75:
		return sWarn
	}
	return sGood
}

func (s *dashboardScreen) tiles() []func(w, h int) string {
	st := s.st
	cpu := s.cpuFrac()
	mem := s.memFrac()
	d, df := s.rootDisk()
	running, stopped, unhealthy, _ := s.counts()
	total := len(st.Containers)
	cf := 0.0
	if total > 0 {
		cf = float64(running-unhealthy) / float64(total)
	}
	cnote, cstyle, cborder := "all running", sGood, cFaint
	switch {
	case stopped > 0 && unhealthy > 0:
		cnote, cstyle, cborder = fmt.Sprintf("%d stopped, %d unhealthy", stopped, unhealthy), sBad, cRed
	case stopped > 0:
		cnote, cstyle, cborder = fmt.Sprintf("%d stopped", stopped), sBad, cRed
	case unhealthy > 0:
		cnote, cstyle, cborder = fmt.Sprintf("%d unhealthy", unhealthy), sWarn, cAmber
	case total == 0:
		cnote = "none yet"
	}
	return []func(w, h int) string{
		tile("CPU", pct(cpu), fmt.Sprintf("load %.2f on %d cores", st.Load[0], st.CPUs), cpu, usageStyle(cpu), usageColor(cpu)),
		tile("Memory", pct(mem), deploy.HumanSize(st.MemTotal-st.MemAvail)+" of "+deploy.HumanSize(st.MemTotal), mem, usageStyle(mem), usageColor(mem)),
		tile("Disk "+d.Mount, pct(df), deploy.HumanSize(d.Avail)+" free", df, usageStyle(df), usageColor(df)),
		tile("Containers", fmt.Sprintf("%d/%d", running, total), cnote, cf, cstyle, cborder),
	}
}

// compactMeters is the tiles as lines, for a phone.
func (s *dashboardScreen) compactMeters(w int) string {
	st := s.st
	barW := max(6, w-24)
	row := func(k string, frac float64, v string) string {
		return lipgloss.NewStyle().Width(8).Render(sMuted.Render(k)) + meter(frac, barW) + " " + sTitle.Render(v)
	}
	d, df := s.rootDisk()
	running, _, _, _ := s.counts()
	return strings.Join([]string{
		row("CPU", s.cpuFrac(), pct(s.cpuFrac())),
		row("Memory", s.memFrac(), pct(s.memFrac())),
		row("Disk", df, deploy.HumanSize(d.Avail)+" free"),
		lipgloss.NewStyle().Width(8).Render(sMuted.Render("Running")) + sTitle.Render(fmt.Sprintf("%d of %d", running, len(st.Containers))),
	}, "\n")
}

// systemCard has the numbers behind the tiles, plus swap and backups.
func (s *dashboardScreen) systemCard(w, h int) string {
	st := s.st
	inner := w - 4
	kw := 13
	kv := func(k, v string) string { return lipgloss.NewStyle().Width(kw).Render(sMuted.Render(k)) + v }
	barW := max(6, min(24, inner-kw-22))
	var lines []string
	if st.CPUs > 0 {
		note := ""
		if inner >= 50 {
			note = "  1, 5, 15 min"
		}
		lines = append(lines, kv("Load", sTitle.Render(fmt.Sprintf("%.2f  %.2f  %.2f", st.Load[0], st.Load[1], st.Load[2]))+sFaint.Render(note)))
	}
	if st.MemTotal > 0 {
		used := st.MemTotal - st.MemAvail
		lines = append(lines, kv("Memory", meter(s.memFrac(), barW)+" "+sTitle.Render(deploy.HumanSize(used))+sFaint.Render(" / "+deploy.HumanSize(st.MemTotal))))
	}
	if st.SwapTotal > 0 {
		used := st.SwapTotal - st.SwapFree
		lines = append(lines, kv("Swap", meter(float64(used)/float64(st.SwapTotal), barW)+" "+sTitle.Render(deploy.HumanSize(used))+sFaint.Render(" / "+deploy.HumanSize(st.SwapTotal))))
	} else if st.MemTotal > 0 {
		lines = append(lines, kv("Swap", sFaint.Render("none")))
	}
	for _, d := range st.Disks {
		if d.Total == 0 {
			continue
		}
		lines = append(lines, kv("Disk "+d.Mount, meter(float64(d.Used)/float64(d.Total), barW)+" "+sTitle.Render(deploy.HumanSize(d.Avail))+sFaint.Render(" free / "+deploy.HumanSize(d.Total))))
	}
	switch t := st.Timer; {
	case t != "" && t != "n/a" && t != "0":
		lines = append(lines, kv("Next backup", sTitle.Render(t)))
	case s.cfg.Backups.Enabled:
		lines = append(lines, kv("Next backup", sWarn.Render("timer not installed")))
	default:
		lines = append(lines, kv("Next backup", sFaint.Render("not scheduled")))
	}
	return card("System", lines, w, h, cFaint)
}

// historyCard draws load, memory and disk over the days the server has
// noted them, and when the disk fills up at the rate it's growing.
func (s *dashboardScreen) historyCard(w int) string {
	h := s.st.History
	inner := w - 4
	kw := 9
	sw := max(6, inner-kw-24)
	row := func(k string, f func(deploy.Sample) float64, note string) string {
		vals := make([]float64, len(h))
		for i, x := range h {
			vals[i] = f(x)
		}
		last := vals[len(vals)-1]
		return lipgloss.NewStyle().Width(kw).Render(sMuted.Render(k)) + lipgloss.NewStyle().Foreground(usageColor(slices.Max(vals))).Render(sparkline(vals, sw)) +
			" " + sTitle.Render(fmt.Sprintf("%4s", pct(last))) + sFaint.Render(note)
	}
	frac := func(a, b int64) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	peak := func(f func(deploy.Sample) float64) string {
		m := 0.0
		for _, x := range h {
			m = max(m, f(x))
		}
		return "  peak " + pct(m)
	}
	load := func(x deploy.Sample) float64 { return x.Load }
	mem := func(x deploy.Sample) float64 { return frac(x.MemUsed, x.MemTotal) }
	disk := func(x deploy.Sample) float64 { return frac(x.DiskUsed, x.DiskTotal) }
	full := "  not growing"
	if t := s.st.DiskFull(); !t.IsZero() {
		full = fmt.Sprintf("  full in ~%d days", max(1, int(time.Until(t).Hours()/24)))
	}
	days := max(1, int(math.Round(h[len(h)-1].At.Sub(h[0].At).Hours()/24)))
	title := fmt.Sprintf("Last %d days", days)
	if days == 1 {
		title = "Last day"
	}
	lines := []string{row("Load", load, peak(load)), row("Memory", mem, peak(mem)), row("Disk", disk, full)}
	return card(title, lines, w, 0, cFaint)
}

// sparkline draws values from 0 to 1 in w columns, each the highest of
// the values it covers.
func sparkline(vals []float64, w int) string {
	const ticks = "▁▂▃▄▅▆▇█"
	n := min(w, len(vals))
	var b strings.Builder
	for i := range n {
		m := slices.Max(vals[i*len(vals)/n : max(i*len(vals)/n+1, (i+1)*len(vals)/n)])
		t := int(math.Round(min(1, max(0, m)) * 7))
		b.WriteString(string([]rune(ticks)[t]))
	}
	return b.String()
}

// topCard lists what uses the most CPU and memory.
func (s *dashboardScreen) topCard(w, h int) string {
	inner := w - 4
	cs := append([]deploy.Container(nil), s.st.Containers...)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].MemPerc > cs[j].MemPerc })
	var lines []string
	barW := max(4, min(14, inner/4))
	nameW := max(8, inner-barW-16)
	top := 1.0
	if len(cs) > 0 && cs[0].MemPerc > 0 {
		top = cs[0].MemPerc
	}
	for _, c := range cs {
		if len(lines) == 6 || c.State != "running" {
			break
		}
		name := lipgloss.NewStyle().Width(nameW).Render(sTitle.Render(truncate(strings.TrimPrefix(c.Name, "matrix-"), nameW-1)))
		lines = append(lines, name+bar(c.MemPerc/top, barW, sPink)+" "+
			lipgloss.NewStyle().Width(7).Align(lipgloss.Right).Render(sMuted.Render(fmt.Sprintf("%.1f%%", c.MemPerc)))+
			lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Render(sFaint.Render(c.CPU)))
	}
	if len(lines) == 0 {
		lines = append(lines, sFaint.Render("nothing running"))
	} else {
		lines = append([]string{lipgloss.NewStyle().Width(nameW+barW+1).Render("") +
			lipgloss.NewStyle().Width(7).Align(lipgloss.Right).Render(sFaint.Render("mem")) +
			lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Render(sFaint.Render("cpu"))}, lines...)
	}
	total := 0.0
	for _, c := range s.st.Containers {
		total += c.CPUPerc()
	}
	title := "Heaviest"
	if s.st.CPUs > 0 {
		title += sFaint.Render(fmt.Sprintf("  containers use %.0f%% of %d cores", total, s.st.CPUs))
	}
	return card(title, lines, w, h, cFaint)
}

func (s *dashboardScreen) certCard(w int) string {
	inner := w - 4
	certs := s.st.Certs
	svcW := 0
	for _, c := range certs {
		svcW = max(svcW, lipgloss.Width(c.Services)+2)
	}
	svcW = min(svcW, max(12, inner/4))
	hostW := 0
	for _, c := range certs {
		hostW = max(hostW, len(c.Host)+2)
	}
	hostW = min(hostW, max(16, inner/3))
	border := lipgloss.TerminalColor(cFaint)
	var lines []string
	for _, c := range certs {
		style, icon := sGood, "✓"
		switch {
		case c.Err != "", c.DaysLeft() < 7:
			style, icon, border = sBad, "✗", cRed
		case c.DaysLeft() < 21:
			style, icon = sWarn, "!"
			if border != cRed {
				border = cAmber
			}
		}
		state := c.String()
		if c.Err == "" {
			state = fmt.Sprintf("%d days", c.DaysLeft())
			if inner >= svcW+hostW+40 {
				state += sFaint.Render("  until " + c.NotAfter.Local().Format("2006-01-02") + " · " + c.Issuer)
			}
		}
		if inner < 60 {
			lines = append(lines, style.Render(icon)+" "+sTitle.Render(truncate(c.Host, inner-2)),
				"  "+sMuted.Render(truncate(c.Services, inner-4))+"  "+style.Render(state))
			continue
		}
		svc := lipgloss.NewStyle().Width(svcW).Render(sMuted.Render(truncate(c.Services, svcW-2)))
		host := lipgloss.NewStyle().Width(hostW).Render(sTitle.Render(truncate(c.Host, hostW-2)))
		lines = append(lines, style.Render(icon)+" "+svc+host+style.Render(state))
	}
	return card("Certificates", lines, w, 0, border)
}

func (s *dashboardScreen) containerCard(w int, wide bool) string {
	inner := w - 4
	running, stopped, unhealthy, restarted := s.counts()
	title := fmt.Sprintf("Containers  %d of %d running", running, len(s.st.Containers))
	var notes []string
	if stopped > 0 {
		notes = append(notes, sBad.Render(fmt.Sprintf("%d stopped", stopped)))
	}
	if unhealthy > 0 {
		notes = append(notes, sWarn.Render(fmt.Sprintf("%d unhealthy", unhealthy)))
	}
	if restarted > 0 {
		notes = append(notes, sWarn.Render(fmt.Sprintf("%d restarted", restarted)))
	}
	if len(notes) > 0 {
		title += "  " + strings.Join(notes, sFaint.Render(" · "))
	}
	border := lipgloss.TerminalColor(cFaint)
	if stopped > 0 {
		border = cRed
	}
	col := func(wd int, st lipgloss.Style, s string) string {
		return lipgloss.NewStyle().Width(wd).Render(st.Render(truncate(s, wd-1)))
	}
	var lines []string
	showNet, showImg := wide && inner >= 120, wide && inner >= 150
	nameW := min(34, max(14, inner/4))
	for _, c := range s.st.Containers {
		icon := sGood.Render("●")
		stStyle := sMuted
		switch {
		case c.State != "running":
			icon, stStyle = sBad.Render("●"), sBad
		case c.Health == "unhealthy":
			icon, stStyle = sWarn.Render("●"), sWarn
		case c.Health == "starting":
			icon = sWarn.Render("◌")
		}
		name := strings.TrimPrefix(c.Name, "matrix-")
		status := c.Status
		if c.Restarts > 0 {
			status += fmt.Sprintf(" · %d restarts", c.Restarts)
		}
		if inner < 60 {
			lines = append(lines, icon+" "+sTitle.Render(truncate(name, inner-2)),
				"  "+stStyle.Render(truncate(status, inner-2)))
			if c.CPU != "" {
				lines = append(lines, "  "+sFaint.Render(truncate("cpu "+c.CPU+" · mem "+c.Mem, inner-2)))
			}
			continue
		}
		row := icon + " " + col(nameW, sTitle, name) + col(9, sMuted, c.CPU) + col(22, sMuted, c.Mem)
		if showNet {
			row += col(22, sFaint, c.Net)
		}
		if showImg {
			img := c.Image
			if i := strings.LastIndex(img, "/"); i >= 0 {
				img = img[i+1:]
			}
			row += col(30, sFaint, img)
		}
		used := lipgloss.Width(row)
		lines = append(lines, row+stStyle.Render(truncate(status, max(4, inner-used))))
	}
	if inner >= 60 && len(lines) > 0 {
		head := "  " + col(nameW, sFaint, "name") + col(9, sFaint, "cpu") + col(22, sFaint, "memory")
		if showNet {
			head += col(22, sFaint, "network in / out")
		}
		if showImg {
			head += col(30, sFaint, "image")
		}
		lines = append([]string{head + sFaint.Render("status")}, lines...)
	}
	if len(lines) == 0 {
		lines = []string{sFaint.Render("no matrix-* containers yet")}
	}
	return card(title, lines, w, 0, border)
}

func roundDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
