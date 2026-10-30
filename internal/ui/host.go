package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
	"interzone/internal/geo"
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

// hostFrameMsg moves the swarm map's pulses on.
type hostFrameMsg struct{}

func (hostFrameMsg) owner() string { return "Host" }

const hostFrame = 200 * time.Millisecond // five frames a second: each redraws most of the screen

func (hostStatsMsg) owner() string { return "Host" }

// hostStoppedMsg is the end of stopping or removing the node.
type hostStoppedMsg struct {
	removed bool
	err     error
}

func (hostStoppedMsg) owner() string { return "Host" }
func (hostTickMsg) owner() string    { return "Host" }

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

	ledger    map[string]deploy.Peer // each peer, with its bitswap totals, at ledgerAt
	ledgerAt  time.Time
	rates     map[string]peerRate
	peerHist  map[string][]float64 // each peer's recent rates, in and out together
	ripples   []ripple
	comings   []hostComing // newest last
	phase     float64      // seconds into the map's animation
	animating bool         // a hostFrameMsg is on its way

	showForm bool       // on a machine running interzone-mirror's node, s shows the form over the map
	home     *geo.Place // where the user says this machine is, if they did
	placing  bool       // the user is picking home
	placeIn  textinput.Model
	placeSel int

	confirmRemove bool   // ctrl+d asked to remove the node; y does it
	stopping      bool   // stopping or removing it
	note          string // how that went
}

// hostComing is a peer joining or leaving the swarm.
type hostComing struct {
	at   time.Time
	peer deploy.Peer
	join bool
}

// mapMode says the screen is the big map of interzone-mirror's node on
// this machine, which needs no setting up.
func (s *hostScreen) mapMode() bool { return !s.showForm && localMirror.Load() }

func (s *hostScreen) handlesEsc() bool {
	return s.placing || s.confirmRemove || s.showForm && localMirror.Load() && !s.running
}

// mirrorHere says the form's target runs interzone-mirror's node, which
// already hosts everything.
func (s *hostScreen) mirrorHere() bool {
	return s.stats != nil && s.stats.Mirror != "" && s.statsFor == targetKey(s.target())
}

// hosting says the form's target runs a node interzone set up, which
// ctrl+x can stop and ctrl+d remove.
func (s *hostScreen) hosting() bool {
	return s.stats != nil && s.stats.Installed && s.stats.Mirror == "" && s.statsFor == targetKey(s.target())
}

// moving says whether data is moving with any peer, so the map animates.
func (s *hostScreen) moving() bool {
	for _, r := range s.rates {
		if r.in+r.out > 0 {
			return true
		}
	}
	return false
}

func (s *hostScreen) frame() tea.Cmd {
	if s.animating || !s.visible || !s.moving() && !s.mapMode() {
		return nil
	}
	s.animating = true
	return tea.Tick(hostFrame, func(time.Time) tea.Msg { return hostFrameMsg{} })
}

// readLedgers turns the peers' bitswap totals into rates since the last
// reading, and notes who joined and left.
func (s *hostScreen) readLedgers(st *deploy.HostStats, now time.Time) {
	secs := now.Sub(s.ledgerAt).Seconds()
	next := map[string]deploy.Peer{}
	hist := map[string][]float64{}
	s.rates = map[string]peerRate{}
	came := func(p deploy.Peer, join bool) {
		s.comings = append(s.comings, hostComing{now, p, join})[max(0, len(s.comings)+1-50):]
		s.ripples = append(s.ripples, ripple{addr: p.Addr, start: s.phase, join: join})
	}
	for _, p := range st.PeerList {
		next[p.ID] = p
		was, ok := s.ledger[p.ID]
		switch {
		case ok && secs > 0:
			r := peerRate{in: max(0, float64(p.Recv-was.Recv)/secs), out: max(0, float64(p.Sent-was.Sent)/secs)}
			s.rates[p.ID] = r
			hist[p.ID] = append(s.peerHist[p.ID], r.in+r.out)[max(0, len(s.peerHist[p.ID])+1-16):]
		case !ok && s.ledger != nil:
			came(p, true)
		}
	}
	for id, p := range s.ledger {
		if _, ok := next[id]; !ok {
			came(p, false)
		}
	}
	s.ledger, s.ledgerAt, s.peerHist = next, now, hist
}

func newHostScreen() *hostScreen {
	f := newForm(
		toggleField("part:oci", "Host the container images (about 12 GB)", true,
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
	in := textinput.New()
	in.Prompt = sAccent.Render("› ")
	in.Placeholder = "a town, e.g. Bushehr"
	in.PlaceholderStyle = sFaint
	return &hostScreen{form: f, spin: newSpinner(), home: loadHome(), placeIn: in}
}

func (s *hostScreen) name() string { return "Host" }
func (s *hostScreen) title() string {
	if s.mapMode() {
		return "interzone-mirror's IPFS swarm"
	}
	return "Host interzone's files over IPFS"
}
func (s *hostScreen) typing() bool      { return !s.running && (!s.mapMode() || s.placing) }
func (s *hostScreen) activeForm() *form { return s.form }
func (s *hostScreen) keys() [][2]string {
	if s.running {
		return [][2]string{{"ctrl+x", "stop"}, {"↑↓", "scroll"}}
	}
	if s.mapMode() && s.placing {
		return [][2]string{{"enter", "pick"}, {"↑↓", "choose"}, {"esc", "cancel"}}
	}
	if s.mapMode() {
		return [][2]string{{"l", "where this node is"}, {"s", "set up hosting elsewhere"}}
	}
	back := [][2]string{}
	if localMirror.Load() {
		back = [][2]string{{"esc", "the map"}}
	}
	if s.mirrorHere() {
		return append([][2]string{{"↑↓", "move"}}, back...)
	}
	if s.confirmRemove {
		return [][2]string{{"y", "remove it"}, {"esc", "cancel"}}
	}
	keys := [][2]string{{"enter", "start hosting"}, {"↑↓", "move"}}
	if s.hosting() && s.stats.Active {
		keys = append(keys, [2]string{"ctrl+x", "stop sharing"})
	}
	if s.hosting() {
		keys = append(keys, [2]string{"ctrl+d", "remove"})
	}
	return append(keys, back...)
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
	return tea.Batch(s.poll(), s.frame())
}

func (s *hostScreen) leave(*config.Config) { s.visible = false }

func (s *hostScreen) target() config.Target {
	if s.mapMode() {
		return config.Target{Mode: "local", User: "root"}
	}
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
// machine is only asked if it hosts or runs Docker (interzone-mirror's
// node); an SSH server once its address is in.
func (s *hostScreen) poll() tea.Cmd {
	t := s.target()
	if s.polling || t.Mode == "ssh" && t.Host == "" {
		return nil
	}
	if t.Mode != "ssh" {
		_, unit := os.Stat("/etc/systemd/system/interzone-ipfs.service")
		_, docker := os.Stat("/var/run/docker.sock")
		if unit != nil && docker != nil {
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
			s.in, s.out, s.ledger, s.rates, s.peerHist, s.comings, s.ripples = nil, nil, nil, nil, nil, nil, nil
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
		s.readLedgers(m.stats, time.Now())
		if !s.visible {
			return nil
		}
		return tea.Batch(s.tick(5*time.Second), s.frame())
	case hostFrameMsg:
		s.animating = false
		s.phase += hostFrame.Seconds()
		s.ripples = slices.DeleteFunc(s.ripples, func(r ripple) bool { return s.phase-r.start > rippleSecs })
		if s.mapMode() && s.statsFor != targetKey(s.target()) {
			return tea.Batch(s.frame(), s.poll()) // the mirror was found after the screen opened
		}
		return s.frame()
	case hostTickMsg:
		if s.visible && m.n == s.ticks {
			return s.poll()
		}
		return nil
	case hostStoppedMsg:
		s.stopping = false
		switch {
		case m.err != nil:
			s.err = m.err
		case m.removed:
			s.note = "✓ removed, with everything it downloaded. enter sets hosting up again."
		default:
			s.note = "✓ stopped sharing; it stays off after a reboot."
		}
		return s.tick(0)
	case spinner.TickMsg:
		return spinning(s.running || s.stopping, &s.spin, m)
	case tea.KeyMsg:
		if s.stopping {
			return nil
		}
		if s.confirmRemove {
			s.confirmRemove = false
			if m.String() == "y" {
				return s.stop(true)
			}
			return nil
		}
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
		if s.mapMode() {
			if s.placing {
				return s.placeKey(m)
			}
			if m.String() == "l" {
				s.placing, s.placeSel = true, 0
				s.placeIn.SetValue("")
				return s.placeIn.Focus()
			}
			if m.String() == "s" {
				s.showForm = true
				return s.tick(0)
			}
			return nil
		}
		if m.String() == "esc" && s.handlesEsc() {
			s.showForm, s.err = false, nil
			return tea.Batch(s.tick(0), s.frame())
		}
		if m.String() == "ctrl+x" && s.hosting() && s.stats.Active {
			return s.stop(false)
		}
		if m.String() == "ctrl+d" && s.hosting() {
			s.confirmRemove, s.err, s.note = true, nil, ""
			return nil
		}
		if m.String() == "enter" {
			s.note = ""
			if s.mirrorHere() {
				s.err = fmt.Errorf("%s already hosts everything here: interzone-mirror's node, below", map[bool]string{true: "this machine", false: s.target().Host}[s.target().Mode != "ssh"])
				return nil
			}
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

// stop stops the node, or removes it, on the form's target.
func (s *hostScreen) stop(remove bool) tea.Cmd {
	t := s.target()
	s.stopping, s.err, s.note = true, nil, ""
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return hostStoppedMsg{remove, deploy.StopHosting(ctx, t, remove)}
	})
}

// tick asks for a reading after d, replacing any tick asked for before.
func (s *hostScreen) tick(d time.Duration) tea.Cmd {
	s.ticks++
	n := s.ticks
	return tea.Tick(d, func(time.Time) tea.Msg { return hostTickMsg{n} })
}

func (s *hostScreen) view(w, h int) string {
	if s.mapMode() {
		return s.mapView(w, h)
	}
	intro := sSub.Width(min(w, maxFormWidth)).Render("Help servers set up and update during a blackout: the machine keeps the newest " +
		"signed copy of what interzone downloads and shares it with interzone's IPFS swarm. It listens on TCP port 4001 " +
		"(open it in the firewall), and the swarm's members can see that it holds Matrix software.")
	var status []string
	switch {
	case s.running:
		status = append(status, s.spin.View()+sMuted.Render(" setting up… ctrl+x stops"))
	case s.stopping:
		status = append(status, s.spin.View()+sMuted.Render(" stopping the node…"))
	case s.confirmRemove:
		size := ""
		if s.stats != nil && s.stats.RepoSize > 0 {
			size = " (" + humanBytes(s.stats.RepoSize) + ")"
		}
		status = append(status, strings.Split(confirmView(min(w, maxFormWidth),
			sWarn.Render("Remove hosting from "+map[bool]string{true: "this machine", false: s.target().Host}[s.target().Mode != "ssh"]+"?"),
			sSub.Render("Stops the IPFS node and deletes it with everything it downloaded"+size+". A Matrix server installed there keeps running; a later deploy in IPFS mode installs the node again."),
			"", confirmKeys("y", "remove it")), "\n")...)
	case s.err != nil:
		status = append(status, sBad.Render("✗ "+s.err.Error()))
	case s.note != "":
		status = append(status, sGood.Render(s.note))
	case s.done:
		status = append(status, sGood.Render("✓ hosting. It keeps going without interzone; enter sets it up again."))
	}
	for _, l := range s.log {
		status = append(status, strings.Split(sMuted.Width(min(w, maxFormWidth)).Render("  "+l), "\n")...)
	}
	status = append(status, s.statsView(w)...)
	// The swarm map, below the numbers, if it still leaves the form 8 rows.
	if room := h - lipgloss.Height(intro) - len(status) - 3 - 8 - 1; s.statsActive() && room >= 12 {
		status = append(status, "")
		status = append(status, swarmMap(mapScene{st: s.stats, rates: s.rates, phase: s.phase, ripples: s.ripples}, min(w, maxFormWidth), min(room, 24))...)
	}
	bottom := strings.Join(status, "\n")
	formH := max(6, h-lipgloss.Height(intro)-lipgloss.Height(bottom)-3)
	if s.running || s.done {
		formH = min(formH, 6)
	}
	return intro + "\n\n" + s.form.View(w, formH) + "\n\n" + bottom
}

// statsActive says the form's target runs a node, so there's a swarm to draw.
func (s *hostScreen) statsActive() bool {
	return s.stats != nil && s.stats.Installed && s.stats.Active && s.statsFor == targetKey(s.target())
}

// statsView is the hosting node's numbers, when the form's target hosts.
func (s *hostScreen) statsView(w int) []string {
	st := s.stats
	if st == nil || !st.Installed || s.statsFor != targetKey(s.target()) {
		return nil
	}
	w = min(w, maxFormWidth)
	where := map[bool]string{true: "this machine", false: s.target().Host}[s.target().Mode != "ssh"]
	heading := "Hosting on " + where
	if st.Mirror != "" {
		heading = "interzone-mirror's node on " + where
	}
	rows := []string{"", sectionHeading(heading, w)}
	row := func(label, value string) {
		rows = append(rows, "  "+lipgloss.NewStyle().Width(12).Render(sMuted.Render(label))+value)
	}
	if !st.Active {
		again := "  enter starts it again"
		if st.Mirror != "" {
			again = "  docker start " + st.Mirror + " starts it again"
		}
		row("Status", sBad.Render("● stopped")+sFaint.Render(again))
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
	row("Download", sAccent.Render(sparkOf(s.in, 20))+sMuted.Render(fmt.Sprintf("  %s/s · %s in all", humanBytes(int64(st.RateIn)), humanBytes(st.TotalIn))))
	row("Upload", sAccent.Render(sparkOf(s.out, 20))+sMuted.Render(fmt.Sprintf("  %s/s · %s in all", humanBytes(int64(st.RateOut)), humanBytes(st.TotalOut))))
	row("Swarm", sMuted.Render(fmt.Sprintf("gave %s (%d blocks) · got %s (%d blocks)",
		humanBytes(st.DataSent), st.BlocksSent, humanBytes(st.DataReceived), st.BlocksReceived)))
	row("Disk", sMuted.Render(fmt.Sprintf("%s used · %s free", humanBytes(st.RepoSize), humanBytes(st.DiskFree))))
	if st.DiskFree > 0 && st.Wanted > st.Received && st.DiskFree < st.Wanted-st.Received {
		row("", sBad.Render("✗ not enough free space for the rest"))
	}
	update := sMuted.Render("waiting")
	if st.Mirror != "" {
		update = sMuted.Render("publishes after each images run")
	}
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
		update += sFaint.Render(" · the publish of " + publishDate(st.Published))
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
