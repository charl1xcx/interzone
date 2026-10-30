package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
	"interzone/internal/ipfs"
)

// ── Host ───────────────────────────────────────────────────────────
//
// Anyone can help interzone's IPFS swarm without a homeserver: this
// machine or a server over SSH keeps the newest publish (everything, or
// the images) and shares it (deploy.ActionHost). The run has its own
// target, so it doesn't touch the active server's answers.

type hostEventMsg struct {
	gen int
	ev  deploy.Event
	ok  bool
}

func (hostEventMsg) owner() string { return "Host" }

// hostStatsMsg is a reading of the hosting node; hostTickMsg asks for the next.
type hostStatsMsg struct {
	target string
	stats  *deploy.HostStats
	err    error
}
type hostTickMsg struct{ n int }

func (hostStatsMsg) owner() string { return "Host" }
func (hostTickMsg) owner() string  { return "Host" }

const hostHistory = 60 // readings kept for the traffic sparklines

type hostScreen struct {
	form    *form
	events  <-chan deploy.Event
	cancel  context.CancelFunc
	gen     int
	running bool
	done    bool
	err     error
	log     []string
	spin    spinner.Model
	scroll  int

	visible  bool
	polling  bool
	stats    *deploy.HostStats
	statsFor string    // the target the stats are of
	ticks    int       // the newest tick asked for; older ones are dropped
	in, out  []float64 // recent rates, bytes a second
}

func newHostScreen() *hostScreen {
	f := newForm(
		toggleField("part:oci", "Host the container images (about 16 GB)", true,
			"Host any mix of parts. All four let other servers set up from scratch through yours; "+
				"new publishes replace the old one every hour."),
		toggleField("part:packages", "Host the OS, Docker and Python packages (about 3.7 GB)", true, ""),
		toggleField("part:git", "Host the playbook and its roles in git (about 50 MB)", true, ""),
		toggleField("part:bin", "Host interzone and IPFS themselves (about 230 MB)", true, ""),
		choiceField("mode", "Host where?", []string{"local", "ssh"},
			[]string{"This machine", "A server over SSH"}, "local",
			"A Linux machine with systemd, x86 or ARM. It needs no homeserver."),
		textField("host", "Server address", "", "203.0.113.10 or server.example.com", "IP or hostname reachable over SSH."),
		textField("user", "SSH user", "", "root", ""),
		textField("port", "SSH port", "", "22", ""),
		textField("key", "SSH private key", "", "~/.ssh/id_ed25519", "Leave empty to use your SSH agent or default key."),
		secretField("become", "sudo password", "", "Needed if you're not root and sudo asks for a password."),
	)
	f.get("host").required = true
	remote := func() bool { return f.value("mode") != "ssh" }
	for _, k := range []string{"host", "user", "port", "key"} {
		f.get(k).hidden = remote
	}
	return &hostScreen{form: f, spin: newSpinner()}
}

func (s *hostScreen) name() string      { return "Host" }
func (s *hostScreen) title() string     { return "Host interzone's files over IPFS" }
func (s *hostScreen) typing() bool      { return !s.running }
func (s *hostScreen) activeForm() *form { return s.form }
func (s *hostScreen) keys() [][2]string {
	if s.running {
		return [][2]string{{"ctrl+x", "stop"}, {"↑↓", "scroll"}}
	}
	return [][2]string{{"enter", "start hosting"}, {"↑↓", "move"}}
}

// enter offers the active server's SSH details, the likeliest machine,
// and reads the node there if it hosts.
func (s *hostScreen) enter(c *config.Config) tea.Cmd {
	if s.form.value("host") == "" && c.Target.Mode == "ssh" {
		s.form.get("mode").choice = 1
		for k, v := range map[string]string{"host": c.Target.Host, "user": c.Target.User, "port": c.Target.Port, "key": c.Target.SSHKey} {
			s.form.get(k).input.SetValue(v)
		}
	}
	s.visible = true
	return s.poll()
}

func (s *hostScreen) leave(*config.Config) { s.visible = false }

func (s *hostScreen) target() config.Target {
	f := s.form
	t := config.Target{Mode: f.value("mode"), Host: strings.TrimSpace(f.value("host")), User: f.value("user"),
		Port: f.value("port"), SSHKey: f.value("key"), BecomePass: f.value("become")}
	if t.User == "" {
		t.User = "root"
	}
	return t
}

func targetKey(t config.Target) string { return t.Mode + " " + t.User + "@" + t.Host + ":" + t.Port }

// poll reads the node's numbers, unless a reading is on its way. This
// machine is only asked if it hosts; an SSH server once its address is in.
func (s *hostScreen) poll() tea.Cmd {
	t := s.target()
	if s.polling || t.Mode == "ssh" && t.Host == "" {
		return nil
	}
	if t.Mode != "ssh" {
		if _, err := os.Stat("/etc/systemd/system/interzone-ipfs.service"); err != nil {
			return nil
		}
	}
	s.polling = true
	key := targetKey(t)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := deploy.ReadHostStats(ctx, t)
		return hostStatsMsg{key, st, err}
	}
}

func (s *hostScreen) start() tea.Cmd {
	cfg := config.Default()
	cfg.Target = s.target()
	if cfg.Target.Mode == "ssh" && cfg.Target.Host == "" {
		s.err = fmt.Errorf("type the server's address first")
		return nil
	}
	var parts []string
	for _, n := range ipfs.PartNames {
		if s.form.get("part:" + n).on {
			parts = append(parts, n)
		}
	}
	if len(parts) == 0 {
		s.err = fmt.Errorf("pick at least one thing to host")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.gen++
	s.cancel, s.running, s.done, s.err, s.log, s.scroll = cancel, true, false, nil, nil, 0
	s.events = deploy.Start(ctx, cfg, deploy.Options{Action: deploy.ActionHost, HostParts: strings.Join(parts, ",")})
	return tea.Batch(s.spin.Tick, s.wait())
}

func (s *hostScreen) wait() tea.Cmd {
	ch, gen := s.events, s.gen
	return func() tea.Msg {
		ev, ok := <-ch
		return hostEventMsg{gen, ev, ok}
	}
}

func (s *hostScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case hostEventMsg:
		if m.gen != s.gen {
			return nil
		}
		if !m.ok {
			s.running = false
			return nil
		}
		switch {
		case m.ev.Err != nil:
			s.err = m.ev.Err
		case m.ev.Done:
			s.done = true
			return tea.Batch(s.wait(), s.poll())
		case m.ev.Line != "":
			s.log = append(s.log, m.ev.Line)
		}
		return s.wait()
	case hostStatsMsg:
		s.polling = false
		if m.target != s.statsFor {
			s.in, s.out = nil, nil
		}
		if m.target != targetKey(s.target()) {
			return s.tick(0) // the form moved on meanwhile
		}
		s.stats, s.statsFor = m.stats, m.target
		if m.err != nil || !m.stats.Installed {
			s.stats = nil
			return nil // nothing hosted there: no need to keep asking
		}
		s.in = append(s.in, m.stats.RateIn)[max(0, len(s.in)+1-hostHistory):]
		s.out = append(s.out, m.stats.RateOut)[max(0, len(s.out)+1-hostHistory):]
		if !s.visible {
			return nil
		}
		return s.tick(5 * time.Second)
	case hostTickMsg:
		if s.visible && m.n == s.ticks {
			return s.poll()
		}
		return nil
	case spinner.TickMsg:
		return spinning(s.running, &s.spin, m)
	case tea.KeyMsg:
		if s.running {
			switch m.String() {
			case "ctrl+x":
				s.cancel()
			case "up":
				s.scroll = max(0, s.scroll-1)
			case "down":
				s.scroll++
			}
			return nil
		}
		if m.String() == "enter" {
			return s.start()
		}
		s.err = nil
	}
	if s.running {
		return nil
	}
	cmd := s.form.Update(msg)
	if _, ok := msg.(tea.KeyMsg); ok && targetKey(s.target()) != s.statsFor {
		return tea.Batch(cmd, s.tick(time.Second)) // once typing pauses
	}
	return cmd
}

// tick asks for a reading after d, replacing any tick asked for before.
func (s *hostScreen) tick(d time.Duration) tea.Cmd {
	s.ticks++
	n := s.ticks
	return tea.Tick(d, func(time.Time) tea.Msg { return hostTickMsg{n} })
}

func (s *hostScreen) view(w, h int) string {
	intro := sSub.Width(min(w, maxFormWidth)).Render("Help servers set up and update during a blackout: the machine keeps the newest " +
		"signed copy of what interzone downloads and shares it with interzone's IPFS swarm. It listens on TCP port 4001 " +
		"(open it in the firewall), and the swarm's members can see that it holds Matrix software.")
	var status []string
	switch {
	case s.running:
		status = append(status, s.spin.View()+sMuted.Render(" setting up… ctrl+x stops"))
	case s.err != nil:
		status = append(status, sBad.Render("✗ "+s.err.Error()))
	case s.done:
		status = append(status, sGood.Render("✓ hosting. It keeps going without interzone; enter sets it up again."))
	}
	for _, l := range s.log {
		status = append(status, strings.Split(sMuted.Width(min(w, maxFormWidth)).Render("  "+l), "\n")...)
	}
	status = append(status, s.statsView(w)...)
	bottom := strings.Join(status, "\n")
	formH := max(6, h-lipgloss.Height(intro)-lipgloss.Height(bottom)-3)
	if s.running || s.done {
		formH = min(formH, 6)
	}
	return intro + "\n\n" + s.form.View(w, formH) + "\n\n" + bottom
}

// statsView is the hosting node's numbers, when the form's target hosts.
func (s *hostScreen) statsView(w int) []string {
	st := s.stats
	if st == nil || !st.Installed || s.statsFor != targetKey(s.target()) {
		return nil
	}
	w = min(w, maxFormWidth)
	rows := []string{"", sectionHeading("Hosting on "+map[bool]string{true: "this machine", false: s.target().Host}[s.target().Mode != "ssh"], w)}
	row := func(label, value string) {
		rows = append(rows, "  "+lipgloss.NewStyle().Width(12).Render(sMuted.Render(label))+value)
	}
	if !st.Active {
		row("Status", sBad.Render("● stopped")+sFaint.Render("  enter starts it again"))
		return rows
	}
	state := sGood.Render("● sharing") + sMuted.Render(fmt.Sprintf(" · %s · %s", plural(st.Peers, "peer"), strings.Join(partNames(st.Parts), ", ")))
	row("Status", state)
	if st.Wanted > 0 {
		frac := float64(st.Received) / float64(st.Wanted)
		note := "all here"
		if st.Received < st.Wanted {
			note = fmt.Sprintf("%.0f%%", frac*100)
		}
		row("Copy", meter(frac, min(24, w/4))+sMuted.Render(fmt.Sprintf("  %s · %s of %s", note, humanBytes(st.Received), humanBytes(st.Wanted))))
		var parts []string
		for _, p := range st.PublishedParts {
			if !slices.Contains(st.Parts, p.Name) {
				continue
			}
			mark := sFaint.Render("…")
			if p.Pinned {
				mark = sGood.Render("✓")
			}
			parts = append(parts, mark+" "+partNames([]string{p.Name})[0]+" "+sFaint.Render(humanBytes(p.Size)))
		}
		row("Parts", strings.Join(parts, "  "))
	}
	spark := func(vals []float64) string {
		top := 1.0
		for _, v := range vals {
			top = max(top, v)
		}
		scaled := make([]float64, len(vals))
		for i, v := range vals {
			scaled[i] = v / top
		}
		return lipgloss.NewStyle().Width(20).Render(sparkline(scaled, 20)) // fills in as readings come
	}
	row("Download", sAccent.Render(spark(s.in))+sMuted.Render(fmt.Sprintf("  %s/s · %s in all", humanBytes(int64(st.RateIn)), humanBytes(st.TotalIn))))
	row("Upload", sAccent.Render(spark(s.out))+sMuted.Render(fmt.Sprintf("  %s/s · %s in all", humanBytes(int64(st.RateOut)), humanBytes(st.TotalOut))))
	row("Swarm", sMuted.Render(fmt.Sprintf("gave %s (%d blocks) · got %s (%d blocks)",
		humanBytes(st.DataSent), st.BlocksSent, humanBytes(st.DataReceived), st.BlocksReceived)))
	row("Disk", sMuted.Render(fmt.Sprintf("%s used · %s free", humanBytes(st.RepoSize), humanBytes(st.DiskFree))))
	if st.DiskFree > 0 && st.Wanted > st.Received && st.DiskFree < st.Wanted-st.Received {
		row("", sBad.Render("✗ not enough free space for the rest"))
	}
	update := sMuted.Render("waiting")
	switch {
	case st.Following:
		update = sAccent.Render("updating now")
	case st.Failed:
		update = sBad.Render("the last update failed")
	}
	if !st.NextCheck.IsZero() && !st.Following {
		in := "under a minute"
		if d := time.Until(st.NextCheck); d >= time.Minute {
			in = strings.TrimSuffix(d.Round(time.Minute).String(), "0s") // "1h0m", "45m"
		}
		update += sFaint.Render(" · next check in " + in)
	}
	if st.Published != "" {
		update += sFaint.Render(" · the publish of " + strings.NewReplacer("T", " ", "Z", " UTC").Replace(st.Published))
	}
	row("Updates", update)
	if st.Last != "" {
		row("Last", sFaint.Render(truncate(st.Last, max(10, w-16))))
	}
	return rows
}

// partNames says what parts of a publish are, the way the form does.
func partNames(parts []string) []string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = map[string]string{"oci": "images", "packages": "packages", "git": "git", "bin": "programs"}[p]
	}
	return names
}
