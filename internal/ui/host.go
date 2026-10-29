package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
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
}

func newHostScreen() *hostScreen {
	f := newForm(
		choiceField("parts", "What to host", []string{"all", "images"},
			[]string{"Everything (about 19 GB)", "Container images only (about 15 GB)"}, "all",
			"Everything lets other servers set up from scratch through yours: the playbook, OS, Docker and Python packages, and every image. "+
				"New publishes replace the old one every hour."),
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

func (s *hostScreen) name() string         { return "Host" }
func (s *hostScreen) title() string        { return "Host interzone's files over IPFS" }
func (s *hostScreen) typing() bool         { return !s.running }
func (s *hostScreen) leave(*config.Config) {}
func (s *hostScreen) activeForm() *form    { return s.form }
func (s *hostScreen) keys() [][2]string {
	if s.running {
		return [][2]string{{"ctrl+x", "stop"}, {"↑↓", "scroll"}}
	}
	return [][2]string{{"enter", "start hosting"}, {"↑↓", "move"}}
}

// enter offers the active server's SSH details, the likeliest machine.
func (s *hostScreen) enter(c *config.Config) tea.Cmd {
	if s.form.value("host") == "" && c.Target.Mode == "ssh" {
		s.form.get("mode").choice = 1
		for k, v := range map[string]string{"host": c.Target.Host, "user": c.Target.User, "port": c.Target.Port, "key": c.Target.SSHKey} {
			s.form.get(k).input.SetValue(v)
		}
	}
	return nil
}

func (s *hostScreen) start() tea.Cmd {
	f := s.form
	cfg := config.Default()
	cfg.Target = config.Target{Mode: f.value("mode"), Host: strings.TrimSpace(f.value("host")), User: f.value("user"),
		Port: f.value("port"), SSHKey: f.value("key"), BecomePass: f.value("become")}
	if cfg.Target.User == "" {
		cfg.Target.User = "root"
	}
	if cfg.Target.Mode == "ssh" && cfg.Target.Host == "" {
		s.err = fmt.Errorf("type the server's address first")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.gen++
	s.cancel, s.running, s.done, s.err, s.log, s.scroll = cancel, true, false, nil, nil, 0
	s.events = deploy.Start(ctx, cfg, deploy.Options{Action: deploy.ActionHost, HostParts: f.value("parts")})
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
		case m.ev.Line != "":
			s.log = append(s.log, m.ev.Line)
		}
		return s.wait()
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
	return s.form.Update(msg)
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
	bottom := strings.Join(status, "\n")
	formH := max(6, h-lipgloss.Height(intro)-lipgloss.Height(bottom)-3)
	if s.running || s.done {
		formH = min(formH, 6)
	}
	return intro + "\n\n" + s.form.View(w, formH) + "\n\n" + bottom
}
