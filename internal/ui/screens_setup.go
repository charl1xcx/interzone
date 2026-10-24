package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Welcome ────────────────────────────────────────────────────────

type welcomeScreen struct{ loaded bool }

func (s *welcomeScreen) name() string                 { return "Welcome" }
func (s *welcomeScreen) title() string                { return "" }
func (s *welcomeScreen) enter(*config.Config) tea.Cmd { return nil }
func (s *welcomeScreen) leave(*config.Config)         {}
func (s *welcomeScreen) typing() bool                 { return false }
func (s *welcomeScreen) keys() [][2]string            { return [][2]string{{"enter", "begin"}} }
func (s *welcomeScreen) update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "enter" || k.String() == " ") {
		return nav(1)
	}
	return nil
}

func (s *welcomeScreen) view(w, h int) string {
	feature := func(icon, title, text string) string {
		return sPink.Render(icon) + " " + sTitle.Render(title) + "\n  " + sMuted.Render(text)
	}
	head := lipgloss.JoinVertical(lipgloss.Center,
		gradientLines(strings.TrimPrefix(logo, "\n")),
		"",
		sSub.Render("Your own Matrix server, set up with matrix-docker-ansible-deploy,"),
		sSub.Render("without editing a single YAML file."),
	)
	parts := []string{
		head,
		"",
		feature("◆", "Pick any homeserver", "Synapse, Continuwuity, Tuwunel, Conduit or Dendrite."),
		feature("◆", "Opt into anything", fmt.Sprintf("%d bridges, bots, clients and services from the playbook.", len(catalog.Components))),
		feature("◆", "Works during a blackout", "Git, container, Docker, APT and PyPI mirrors inside the country."),
		"",
	}
	if s.loaded {
		parts = append(parts, sGood.Render("✓ Picked up your previous answers."), "")
	}
	parts = append(parts, sAccent.Render("Press enter to begin ›"))
	block := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}

// ── Network & mirrors ──────────────────────────────────────────────

type checksMsg []deploy.Check

type networkScreen struct {
	cfg      *config.Config
	form     *form
	checks   []deploy.Check
	checking bool
	spin     spinner.Model
}

func newNetworkScreen() *networkScreen {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	return &networkScreen{spin: sp}
}

func (s *networkScreen) name() string  { return "Network" }
func (s *networkScreen) title() string { return "Where should everything be downloaded from?" }
func (s *networkScreen) typing() bool  { return s.form.typing() }
func (s *networkScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"←→", "choose"}, {"ctrl+t", "test sources"}, {"enter", "next"}}
}

func (s *networkScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	src := c.Sources
	mode := "online"
	if src.Blackout {
		mode = "blackout"
	}
	f := newForm(
		choiceField("mode", "Network situation", []string{"online", "blackout"},
			[]string{"Normal internet", "Blackout: in-country mirrors only"}, mode,
			"Blackout mode adds mirrors for Docker Engine, OS packages and PyPI."),
		textField("git", "Playbook git repository", src.PlaybookGit, config.DefaultPlaybookGit,
			"Where matrix-docker-ansible-deploy is cloned from."),
		textField("roles", "Git mirror for Ansible roles", src.RolesGitBase, "https://github.com/",
			"Replaces https://github.com/ for the roles in requirements.yml. Use https://github.com/ for upstream."),
		toggleField("useRegistry", "Pull container images from a mirror", src.UseRegistry,
			"Every image (docker.io, ghcr.io, quay.io…) is pulled from one registry."),
		textField("registry", "Container registry mirror", src.RegistryPrefix, config.DefaultRegistryPrefix,
			"Images are pulled as <mirror>/<owner>/<image>, e.g. mechatok.g0th.ir/element-hq/synapse."),
		textField("dockerpkg", "Docker Engine package mirror", src.DockerPackageURL, "https://mirror.example.ir/docker-ce/linux",
			"A mirror of download.docker.com/linux. Leave empty if Docker is already installed on the server."),
		textField("apt", "OS package (APT) mirror", src.APTMirror, "https://mirror.example.ir",
			"The server's Debian/Ubuntu sources are pointed here before installing. Original files are backed up."),
		textField("pypi", "PyPI mirror", src.PyPIIndex, "https://mirror.example.ir/pypi/simple",
			"Used to install Ansible on this machine if it's missing."),
	)
	blackout := func() bool { return f.value("mode") != "blackout" }
	f.get("registry").hidden = func() bool { return !f.get("useRegistry").on }
	f.get("dockerpkg").hidden = blackout
	f.get("apt").hidden = blackout
	f.get("pypi").hidden = blackout
	s.form = f
	return nil
}

func (s *networkScreen) leave(c *config.Config) {
	f := s.form
	c.Sources = config.Sources{
		Blackout:         f.value("mode") == "blackout",
		PlaybookGit:      f.value("git"),
		RolesGitBase:     f.value("roles"),
		UseRegistry:      f.get("useRegistry").on,
		RegistryPrefix:   f.value("registry"),
		DockerPackageURL: f.value("dockerpkg"),
		APTMirror:        f.value("apt"),
		PyPIIndex:        f.value("pypi"),
	}
	if c.Sources.PlaybookGit == "" {
		c.Sources.PlaybookGit = config.DefaultPlaybookGit
	}
	if !c.Sources.Blackout {
		c.Sources.DockerPackageURL, c.Sources.APTMirror, c.Sources.PyPIIndex = "", "", ""
	}
}

func (s *networkScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case checksMsg:
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
		if m.String() == "ctrl+t" && !s.checking {
			cfg := config.Default()
			s.leave(cfg)
			s.checking = true
			s.checks = nil
			return tea.Batch(s.spin.Tick, func() tea.Msg { return checksMsg(deploy.Preflight(cfg)) })
		}
	}
	cmd := s.form.Update(msg)
	s.cfg.Sources.Blackout = s.form.value("mode") == "blackout" // keeps the header badge live
	return cmd
}

func (s *networkScreen) view(w, h int) string {
	results := s.checksView(w)
	formH := h - lipgloss.Height(results) - 1
	return s.form.View(w, formH) + "\n\n" + results
}

func (s *networkScreen) checksView(w int) string {
	if s.checking {
		return s.spin.View() + sMuted.Render(" testing every source…")
	}
	if s.checks == nil {
		return sMuted.Render("Press ") + sKey.Render("ctrl+t") + sMuted.Render(" to test that every source is reachable from here.")
	}
	var lines []string
	for _, c := range s.checks {
		icon, style := sGood.Render("✓"), sMuted
		if !c.OK {
			icon, style = sBad.Render("✗"), sBad
		}
		name := lipgloss.NewStyle().Width(20).Render(sTitle.Render(c.Name))
		lines = append(lines, truncateANSI(icon+" "+name+" "+style.Render(truncate(c.Target, 40)+"  "+c.Detail), w))
	}
	return strings.Join(lines, "\n")
}

// ── Server ─────────────────────────────────────────────────────────

type serverScreen struct{ form *form }

func newServerScreen() *serverScreen { return &serverScreen{} }

func (s *serverScreen) name() string  { return "Server" }
func (s *serverScreen) title() string { return "Tell me about your server" }
func (s *serverScreen) typing() bool  { return s.form.typing() }
func (s *serverScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"←→", "choose"}, {"space", "toggle"}, {"ctrl+r", "reveal"}, {"enter", "next"}}
}

func (s *serverScreen) enter(c *config.Config) tea.Cmd {
	t := c.Target
	domain := textField("domain", "Base domain", c.Domain, "example.com",
		"User IDs look like @alice:<domain>. Point matrix.<domain> at your server.")
	domain.required = true
	host := textField("host", "Server address", t.Host, "203.0.113.10 or server.example.com", "IP or hostname reachable over SSH.")
	host.required = true
	sudoHelp := "Needed if you're not root and sudo asks for a password. Leave empty otherwise."
	if os.Geteuid() == 0 {
		sudoHelp = "You're root here, so this is only used for SSH targets."
	}
	f := newForm(
		domain,
		choiceField("mode", "Install where?", []string{"local", "ssh"},
			[]string{"This machine", "A remote server over SSH"}, t.Mode,
			"Run interzone on the server itself, or from your computer over SSH."),
		host,
		textField("user", "SSH user", t.User, "root", ""),
		textField("port", "SSH port", t.Port, "22", ""),
		textField("key", "SSH private key", t.SSHKey, "~/.ssh/id_ed25519", "Leave empty to use your SSH agent or default key."),
		secretField("become", "sudo password", t.BecomePass, sudoHelp),
		toggleField("federation", "Federate with other Matrix servers", c.Federation,
			"Turn off for a private server that never talks to the outside world."),
		toggleField("ipv6", "Enable IPv6 networking for containers", c.IPv6, "Only if your server has working IPv6."),
	)
	remote := func() bool { return f.value("mode") != "ssh" }
	for _, k := range []string{"host", "user", "port", "key"} {
		f.get(k).hidden = remote
	}
	f.get("federation").section = "Networking"
	s.form = f
	return nil
}

func (s *serverScreen) leave(c *config.Config) {
	f := s.form
	c.Domain = strings.ToLower(strings.TrimPrefix(f.value("domain"), "matrix."))
	c.Target = config.Target{
		Mode: f.value("mode"), Host: f.value("host"), User: f.value("user"),
		Port: f.value("port"), SSHKey: f.value("key"), BecomePass: f.value("become"),
	}
	if c.Target.User == "" {
		c.Target.User = "root"
	}
	c.Federation = f.get("federation").on
	c.IPv6 = f.get("ipv6").on
}

func (s *serverScreen) update(msg tea.Msg) tea.Cmd { return s.form.Update(msg) }
func (s *serverScreen) view(w, h int) string       { return s.form.View(w, h) }

// ── Homeserver ─────────────────────────────────────────────────────

type homeserverScreen struct{ sel int }

func (s *homeserverScreen) name() string  { return "Homeserver" }
func (s *homeserverScreen) title() string { return "Choose your homeserver" }
func (s *homeserverScreen) typing() bool  { return false }
func (s *homeserverScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "select"}, {"enter", "choose"}}
}

func (s *homeserverScreen) enter(c *config.Config) tea.Cmd {
	for i, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			s.sel = i
		}
	}
	return nil
}

func (s *homeserverScreen) leave(c *config.Config) { c.Homeserver = catalog.Homeservers[s.sel].ID }

func (s *homeserverScreen) update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		n := len(catalog.Homeservers)
		switch k.String() {
		case "up", "k", "shift+tab":
			s.sel = (s.sel + n - 1) % n
		case "down", "j", "tab":
			s.sel = (s.sel + 1) % n
		case "enter", " ":
			return nav(1)
		}
	}
	return nil
}

func (s *homeserverScreen) view(w, h int) string {
	cardW := min(w, 76)
	compact := h < len(catalog.Homeservers)*5
	var cards []string
	for i, hs := range catalog.Homeservers {
		name := sTitle.Render(hs.Name)
		tag := sMuted.Render("  " + hs.Tagline)
		style := sPanel.Width(cardW - 2)
		if i == s.sel {
			style = sPanelFocus.Width(cardW - 2)
			name = sAccent.Render("◉ ") + gradient(hs.Name)
		} else {
			name = sFaint.Render("○ ") + name
		}
		body := name + tag
		if !compact || i == s.sel {
			body += "\n" + sSub.Render(hs.Description)
		}
		if compact && i != s.sel {
			cards = append(cards, "  "+sMuted.Render("○ ")+sMuted.Render(hs.Name)+tag)
			continue
		}
		cards = append(cards, style.Render(body))
	}
	return lipgloss.JoinVertical(lipgloss.Left, cards...)
}

// ── SSL ────────────────────────────────────────────────────────────

type sslScreen struct {
	form     *form
	blackout bool
}

func newSSLScreen() *sslScreen { return &sslScreen{} }

func (s *sslScreen) name() string  { return "Certificates" }
func (s *sslScreen) title() string { return "How should HTTPS certificates be handled?" }
func (s *sslScreen) typing() bool  { return s.form.typing() }
func (s *sslScreen) keys() [][2]string {
	return [][2]string{{"←→", "choose"}, {"↑↓", "move"}, {"enter", "next"}}
}

var sslModes = []string{"letsencrypt", "selfsigned", "custom", "none"}
var sslLabels = []string{"Let's Encrypt", "Self-signed", "My own certificate", "No HTTPS"}
var sslExplain = map[string]string{
	"letsencrypt": "Free, trusted certificates issued automatically. Needs access to letsencrypt.org, so it won't work during a blackout.",
	"selfsigned":  "interzone generates a 10-year wildcard certificate locally. Works fully offline; clients will have to trust it manually.",
	"custom":      "Use a certificate you already have (e.g. from a national CA). Point to the PEM files on this machine.",
	"none":        "Plain HTTP. Only if another reverse proxy in front of this server terminates TLS.",
}

func (s *sslScreen) enter(c *config.Config) tea.Cmd {
	s.blackout = c.Sources.Blackout
	mode := c.SSL.Mode
	if s.blackout && mode == "letsencrypt" {
		mode = "selfsigned"
	}
	f := newForm(
		choiceField("mode", "Certificate source", sslModes, sslLabels, mode, ""),
		textField("email", "Contact email for Let's Encrypt", c.SSL.ACMEEmail, "you@example.com", "Optional. Used for expiry warnings."),
		textField("cert", "Certificate (fullchain PEM)", c.SSL.CertPath, "/path/to/fullchain.pem", ""),
		textField("key", "Private key (PEM)", c.SSL.KeyPath, "/path/to/privkey.pem", ""),
	)
	f.get("email").hidden = func() bool { return f.value("mode") != "letsencrypt" }
	custom := func() bool { return f.value("mode") != "custom" }
	f.get("cert").hidden, f.get("key").hidden = custom, custom
	f.get("cert").required, f.get("key").required = true, true
	s.form = f
	return nil
}

func (s *sslScreen) leave(c *config.Config) {
	c.SSL = config.SSL{Mode: s.form.value("mode"), ACMEEmail: s.form.value("email"),
		CertPath: s.form.value("cert"), KeyPath: s.form.value("key")}
}

func (s *sslScreen) update(msg tea.Msg) tea.Cmd { return s.form.Update(msg) }

func (s *sslScreen) view(w, h int) string {
	mode := s.form.value("mode")
	info := sPanel.Width(min(w, 80) - 2).Render(sSub.Render(sslExplain[mode]))
	if s.blackout && mode == "letsencrypt" {
		info += "\n" + sWarn.Render("⚠ You chose blackout mode: Let's Encrypt will fail without international internet.")
	}
	return s.form.View(w, h-lipgloss.Height(info)-1) + "\n\n" + info
}
