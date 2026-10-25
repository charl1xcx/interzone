package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Welcome ────────────────────────────────────────────────────────

type welcomeScreen struct {
	loaded    bool
	installed bool // a previous run set up this server
	domain    string
	choice    int // 0: manage, 1: change the setup
}

func (s *welcomeScreen) name() string  { return "Welcome" }
func (s *welcomeScreen) title() string { return "" }
func (s *welcomeScreen) enter(c *config.Config) tea.Cmd {
	s.installed, s.domain = c.Installed(), c.Domain
	return nil
}
func (s *welcomeScreen) leave(*config.Config) {}
func (s *welcomeScreen) typing() bool         { return false }
func (s *welcomeScreen) keys() [][2]string {
	if s.installed {
		return [][2]string{{"↑↓", "choose"}, {"enter", "continue"}, {"s", "servers"}}
	}
	return [][2]string{{"enter", "begin"}, {"i", "import a setup"}, {"s", "servers"}}
}
func (s *welcomeScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "up", "k", "down", "j", "tab", "shift+tab":
		s.choice = 1 - s.choice
	case "enter", " ":
		if s.installed && s.choice == 0 {
			return func() tea.Msg { return gotoMsg("Manage") }
		}
		return nav(1)
	case "s":
		return func() tea.Msg { return gotoMsg("Servers") }
	case "i":
		return tea.Sequence(func() tea.Msg { return gotoMsg("Servers") }, func() tea.Msg { return importOpenMsg{} })
	}
	return nil
}

func (s *welcomeScreen) view(w, h int) string {
	tw := min(w, 84) // text width
	// wrap renders text tw wide with every line after indent.
	wrap := func(st lipgloss.Style, indent, text string) string {
		t := st.Width(max(10, tw-lipgloss.Width(indent))).Render(text)
		return strings.ReplaceAll(t, "\n", "\n"+indent)
	}
	feature := func(icon, title, text string) string {
		return sPink.Render(icon) + "  " + sTitle.Render(title) + "\n   " + wrap(sMuted, "   ", text)
	}
	brand := gradientLines(strings.TrimPrefix(logo, "\n"))
	if lipgloss.Width(brand) > w {
		brand = gradient("◢◤ interzone")
	}
	head := lipgloss.JoinVertical(lipgloss.Center,
		brand,
		"",
		sSub.Width(min(tw, 66)).Align(lipgloss.Center).Render("Your own Matrix server, set up with matrix-docker-ansible-deploy, without editing a single YAML file."),
	)
	features := []string{
		feature("◆", "Pick any homeserver", "Synapse, Continuwuity, Tuwunel, Conduit or Dendrite."),
		"",
		feature("◆", "Opt into anything", fmt.Sprintf("%d bridges, bots, clients and services from the playbook.", len(catalog.Components))),
		"",
		feature("◆", "Works during a blackout", "Git, container, Docker, OS package and PyPI mirrors inside the country."),
		"",
		"",
	}
	var tail []string
	switch {
	case s.installed:
		opt := func(i int, title, desc string) string {
			id := "welcome:" + strconv.Itoa(i)
			if i == s.choice {
				bar := sAccent.Render("▌") + "    "
				return markRow(id, sAccent.Render("▌ ◉  ")+sTitle.Render(title)+"\n"+bar+wrap(sMuted, bar, desc), tw)
			}
			return markRow(id, "  "+sFaint.Render("○  ")+sMuted.Render(title)+"\n"+"     "+wrap(sFaint, "     ", desc), tw)
		}
		tail = append(tail,
			wrap(lipgloss.NewStyle(), "", sGood.Render("✓ This computer already set up ")+sTitle.Render(s.domain)),
			"", "",
			opt(0, "Manage the server", "Update, check, users, backups, dashboard, rollback, logs…"),
			"",
			opt(1, "Change the setup", "Walk through the wizard again with your saved answers."))
	case s.loaded:
		tail = append(tail, sGood.Render("✓ Picked up your previous answers."), "", "", mark("key:enter", sAccent.Render("Press enter to begin ›")))
	default:
		tail = append(tail, mark("key:enter", sAccent.Render("Press enter to begin ›")), "",
			mark("key:i", wrap(sMuted, "", "Already set up a server with the playbook yourself? Press i to import it.")))
	}
	parts := append([]string{head, "", ""}, append(features, tail...)...)
	block := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if lipgloss.Height(block) > h { // short or narrow: the choices matter more
		block = lipgloss.JoinVertical(lipgloss.Left, append([]string{head, "", ""}, tail...)...)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}

// ── Server ─────────────────────────────────────────────────────────
//
// One form for where everything goes and comes from: the server, where
// downloads come from (normal internet or in-country mirrors), HTTPS
// certificates, and networking.

type checksMsg []deploy.Check

type serverScreen struct {
	cfg      *config.Config
	form     *form
	checks   []deploy.Check
	checking bool
	spin     spinner.Model
}

func newServerScreen() *serverScreen {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	return &serverScreen{spin: sp}
}

func (s *serverScreen) name() string  { return "Server" }
func (s *serverScreen) title() string { return "Tell me about your server" }
func (s *serverScreen) typing() bool  { return s.form.typing() }
func (s *serverScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"←→", "choose"}, {"space", "toggle"}, {"ctrl+t", "test sources"}, {"ctrl+r", "reveal"}, {"enter", "next"}}
}

var sslModes = []string{"letsencrypt", "selfsigned", "custom", "none"}
var sslLabels = []string{"Let's Encrypt", "Self-signed", "My own certificate", "No HTTPS"}
var sslExplain = map[string]string{
	"letsencrypt": "Free, trusted certificates issued automatically. Needs access to letsencrypt.org, so it won't work during a blackout.",
	"selfsigned":  "interzone generates a 10-year wildcard certificate locally. Works fully offline; clients will have to trust it manually.",
	"custom":      "Use a certificate you already have (e.g. from a national CA). Point to the PEM files on this machine.",
	"none":        "Plain HTTP. Only if another reverse proxy in front of this server terminates TLS.",
}

func (s *serverScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
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

	src := c.Sources
	netMode := "online"
	if src.Blackout {
		netMode = "blackout"
	} else {
		// Blackout fields are cleared outside blackout mode, so switching to it
		// starts from the known in-country mirrors.
		src.DockerPackageURL = config.DefaultMirror(config.MirrorDocker)
		src.APTMirror = config.DefaultMirror(config.MirrorOS)
		src.PyPIIndex = config.DefaultMirror(config.MirrorPyPI)
	}
	sslMode := c.SSL.Mode
	if src.Blackout && sslMode == "letsencrypt" {
		sslMode = "selfsigned"
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

		choiceField("net", "Network situation", []string{"online", "blackout"},
			[]string{"Normal internet", "Blackout: in-country mirrors only"}, netMode,
			"Blackout mode adds mirrors for Docker Engine, OS packages and PyPI."),
		textField("git", "Playbook git repository", src.PlaybookGit, config.DefaultPlaybookGit,
			"Where matrix-docker-ansible-deploy is cloned from."),
		textField("roles", "Git mirror for Ansible roles", src.RolesGitBase, "https://github.com/",
			"Replaces https://github.com/ for the roles in requirements.yml. Use https://github.com/ for upstream."),
		toggleField("useRegistry", "Pull container images from a mirror", src.UseRegistry,
			"Every image (docker.io, ghcr.io, quay.io…) is pulled from one registry."),
		textField("registry", "Container registry mirror", src.RegistryPrefix, config.DefaultRegistryPrefix,
			"Images are pulled as <mirror>/<owner>/<image>, e.g. mechatok.g0th.ir/element-hq/synapse."),
		textField("dockerpkg", "Docker Engine package mirror", src.DockerPackageURL, config.DefaultMirror(config.MirrorDocker),
			"A mirror of download.docker.com/linux. Leave empty if Docker is already installed on the server."+mirrorHint(config.MirrorDocker)),
		textField("apt", "OS package mirror", src.APTMirror, config.DefaultMirror(config.MirrorOS),
			"The server's package sources (Debian, Ubuntu, AlmaLinux, Rocky, CentOS Stream, Fedora, EPEL or Arch) are pointed here before installing, for whatever the mirror carries. Original files are backed up."+mirrorHint(config.MirrorOS)),
		textField("pypi", "PyPI mirror", src.PyPIIndex, config.DefaultMirror(config.MirrorPyPI),
			"Used to install Ansible on this machine if it's missing."+mirrorHint(config.MirrorPyPI)),

		choiceField("ssl", "Certificate source", sslModes, sslLabels, sslMode, ""),
		textField("ssl.email", "Contact email for Let's Encrypt", c.SSL.ACMEEmail, "you@example.com", "Optional. Used for expiry warnings."),
		textField("ssl.cert", "Certificate (fullchain PEM)", c.SSL.CertPath, "/path/to/fullchain.pem", ""),
		textField("ssl.key", "Private key (PEM)", c.SSL.KeyPath, "/path/to/privkey.pem", ""),

		toggleField("federation", "Federate with other Matrix servers", c.Federation,
			"Turn off for a private server that never talks to the outside world."),
		toggleField("ipv6", "Enable IPv6 networking for containers", c.IPv6, "Only if your server has working IPv6."),
	)
	remote := func() bool { return f.value("mode") != "ssh" }
	for _, k := range []string{"host", "user", "port", "key"} {
		f.get(k).hidden = remote
	}
	online := func() bool { return f.value("net") != "blackout" }
	f.get("registry").hidden = func() bool { return !f.get("useRegistry").on }
	f.get("dockerpkg").hidden = online
	f.get("apt").hidden = online
	f.get("pypi").hidden = online
	f.get("ssl.email").hidden = func() bool { return f.value("ssl") != "letsencrypt" }
	custom := func() bool { return f.value("ssl") != "custom" }
	f.get("ssl.cert").hidden, f.get("ssl.key").hidden = custom, custom
	f.get("ssl.cert").required, f.get("ssl.key").required = true, true
	f.get("net").section = "Downloads"
	f.get("ssl").section = "HTTPS certificates"
	f.get("federation").section = "Networking"
	s.form = f
	s.explainSSL()
	return nil
}

// explainSSL makes the certificate choice's help describe the picked one.
func (s *serverScreen) explainSSL() {
	mode := s.form.value("ssl")
	help := sslExplain[mode]
	if mode == "letsencrypt" && s.form.value("net") == "blackout" {
		help += " ⚠ You chose blackout mode: Let's Encrypt will fail without international internet."
	}
	s.form.get("ssl").help = help
}

// mirrorHint describes the known mirrors of kind for a field's help.
func mirrorHint(kind config.MirrorKind) string {
	var parts []string
	for i, m := range config.Mirrors(kind) {
		p := m.URL
		if len(m.Covers) > 0 {
			names := make([]string, len(m.Covers))
			for j, d := range m.Covers {
				names[j] = config.DistroNames[d]
			}
			p += " (" + strings.Join(names, ", ") + ")"
		}
		if i == 0 {
			p += " is the default"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ""
	}
	return " Known mirrors in Iran: " + strings.Join(parts, "; ") + ". Run interzone mirrors to test them."
}

func (s *serverScreen) leave(c *config.Config) {
	f := s.form
	c.Domain = strings.ToLower(f.value("domain"))
	// People often type the homeserver's own name; the base domain is what
	// follows "matrix." (but "matrix.org" itself is a base domain).
	if rest, ok := strings.CutPrefix(c.Domain, "matrix."); ok && strings.Contains(rest, ".") {
		c.Domain = rest
	}
	c.Target = config.Target{
		Mode: f.value("mode"), Host: f.value("host"), User: f.value("user"),
		Port: f.value("port"), SSHKey: f.value("key"), BecomePass: f.value("become"),
	}
	if c.Target.User == "" {
		c.Target.User = "root"
	}
	s.leaveSources(c)
	c.SSL = config.SSL{Mode: f.value("ssl"), ACMEEmail: f.value("ssl.email"),
		CertPath: f.value("ssl.cert"), KeyPath: f.value("ssl.key")}
	c.Federation = f.get("federation").on
	c.IPv6 = f.get("ipv6").on
}

func (s *serverScreen) leaveSources(c *config.Config) {
	f := s.form
	c.Sources = config.Sources{
		Blackout:         f.value("net") == "blackout",
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

func (s *serverScreen) update(msg tea.Msg) tea.Cmd {
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
			s.leaveSources(cfg)
			s.checking = true
			s.checks = nil
			return tea.Batch(s.spin.Tick, func() tea.Msg { return checksMsg(deploy.Preflight(cfg)) })
		}
	}
	cmd := s.form.Update(msg)
	s.cfg.Sources.Blackout = s.form.value("net") == "blackout" // keeps the header badge live
	s.explainSSL()
	return cmd
}

func (s *serverScreen) view(w, h int) string {
	results := s.checksView(w)
	formH := h - lipgloss.Height(results) - 2
	return s.form.View(w, formH) + "\n\n" + results
}

func (s *serverScreen) checksView(w int) string {
	if s.checking {
		return s.spin.View() + sMuted.Render(" testing every source…")
	}
	if s.checks == nil {
		if w < 72 {
			return keyLink("ctrl+t") + sMuted.Render(" tests the download sources")
		}
		return sMuted.Render("Press ") + keyLink("ctrl+t") + sMuted.Render(" to test that every download source is reachable from here.")
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
