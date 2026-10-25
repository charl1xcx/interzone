package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Manage ─────────────────────────────────────────────────────────
//
// Maintenance for a server interzone already set up. It lives outside the
// wizard's steps: Welcome leads here, and actions run on the Deploy screen.

// runActionMsg asks the app to open the Deploy screen and run opts there.
type runActionMsg struct{ opts deploy.Options }

// gotoMsg jumps to the screen with this name.
type gotoMsg string

type servicesMsg struct {
	units []string
	err   error
}

type manageMode int

const (
	modeList manageMode = iota
	modeUser
	modeServices
)

type manageAction struct {
	id, title, desc string
	disabled        string // why it can't be used, if it can't
}

// runsAtOnce are the Manage actions that start changing the server as
// soon as they're chosen; a click on one needs a second click.
var runsAtOnce = map[string]bool{"update": true, "check": true, "restart": true, "backup": true,
	"registration": true, "images": true, "selfupdate": true}

type manageScreen struct {
	cfg    *config.Config
	cursor int
	armed  bool // a click selected a runsAtOnce action; the next click runs it
	mode   manageMode

	userForm *form

	loading  bool
	units    []string
	unitsErr error
	unit     int
	spin     spinner.Model
}

func newManageScreen() *manageScreen {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	return &manageScreen{spin: sp}
}

func (s *manageScreen) name() string { return "Manage" }
func (s *manageScreen) title() string {
	switch s.mode {
	case modeUser:
		return "Add a user"
	case modeServices:
		return "Which service's logs?"
	}
	return "Manage " + s.cfg.Domain
}
func (s *manageScreen) typing() bool { return s.mode == modeUser && s.userForm.typing() }
func (s *manageScreen) keys() [][2]string {
	switch s.mode {
	case modeUser:
		return [][2]string{{"tab/↑↓", "move"}, {"space", "toggle"}, {"ctrl+r", "reveal"}, {"enter", "register"}}
	case modeServices:
		return [][2]string{{"↑↓", "select"}, {"enter", "show logs"}}
	}
	return [][2]string{{"↑↓", "select"}, {"enter", "run"}}
}

// inSubview reports whether esc should return to the action list rather
// than leave the screen.
func (s *manageScreen) inSubview() bool { return s.mode != modeList }

func (s *manageScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.mode, s.armed = modeList, false
	return nil
}

func (s *manageScreen) leave(*config.Config) {}

func (s *manageScreen) actions() []manageAction {
	c := s.cfg
	acts := []manageAction{
		{id: "update", title: "Update the server",
			desc: "Pull the latest playbook and roles, then re-run the install with your saved answers. Also applies any changes you made in the setup."},
		{id: "check", title: "Check services",
			desc: "Run the playbook's self-check: makes sure the homeserver, federation and clients answer over HTTPS."},
		{id: "restart", title: "Restart all services",
			desc: "Restart every Matrix service on the server."},
		{id: "user", title: "Add a user",
			desc: "Register a new account, optionally as a server administrator."},
	}
	if c.Homeserver != "synapse" && c.Homeserver != "dendrite" {
		acts[3].disabled = "Only Synapse and Dendrite can register users from Ansible; use a registration token or open registration instead."
	}
	switch c.Homeserver {
	case "conduit", "continuwuity", "tuwunel":
		if c.RegistrationOpen() {
			acts = append(acts, manageAction{id: "registration", title: "Close registration",
				desc: "Stop new sign-ups now that your account exists. Re-runs the install to apply it."})
		} else {
			acts = append(acts, manageAction{id: "registration", title: "Open registration",
				desc: "Allow sign-ups again (with the registration token where supported). Re-runs the install to apply it."})
		}
	}
	acts = append(acts,
		manageAction{id: "users", title: "Manage users",
			desc: "List every account; reset passwords, grant or remove admin rights, deactivate or reactivate."},
		manageAction{id: "dashboard", title: "Server dashboard",
			desc: "Load, memory, disk space, certificate expiry and the state of every container."},
		manageAction{id: "changes", title: "See what an update brings",
			desc: "Fetch the playbook without installing and list the new commits and component versions."},
		manageAction{id: "images", title: "Check container images",
			desc: "Make sure every image the install needs can be pulled from your registry mirror, for the server's CPU."},
		manageAction{id: "backup", title: "Back up the database",
			desc: "Dump the bundled Postgres database and copy it to " + deploy.BackupDir() + "."},
		manageAction{id: "schedule", title: "Scheduled backups",
			desc: "Have the server dump its database on a timer by itself, keeping the newest few."},
		manageAction{id: "restore", title: "Restore a backup",
			desc: "Replace the database with one of the dumps in " + deploy.BackupDir() + "."},
		manageAction{id: "rollback", title: "Roll back",
			desc: "Go back to the playbook commit and answers of an earlier successful deploy."},
		manageAction{id: "logs", title: "View logs",
			desc: "Show the last 300 log lines of any Matrix service."},
		manageAction{id: "tuning", title: "Fine-tune the roles",
			desc: "Browse and change the default variables of every playbook role: " + homeserverName(c.Homeserver) + ", bridges, Traefik, Postgres and the rest. Applied on the next update."},
		manageAction{id: "setup", title: "Change the setup",
			desc: "Go through the wizard again: components, mirrors, certificates. Saved answers are kept."},
		manageAction{id: "migrate", title: "Move to another server",
			desc: "Copy everything in /matrix to a new machine and install there; the old one is stopped."},
		manageAction{id: "servers", title: "Other servers",
			desc: "Switch between the servers interzone manages, or add another."},
		manageAction{id: "selfupdate", title: "Update interzone",
			desc: "Download the newest interzone release and replace this program with it."},
	)
	for i := range acts {
		switch {
		case acts[i].id == "users" && c.Homeserver != "synapse":
			acts[i].disabled = "Needs Synapse's admin API; " + homeserverName(c.Homeserver) + " doesn't have one."
		case c.Components["external-postgres"] && (acts[i].id == "backup" || acts[i].id == "schedule" || acts[i].id == "restore"):
			acts[i].disabled = "You use an external Postgres server; back it up where it runs."
		}
	}
	return acts
}

func (s *manageScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case servicesMsg:
		s.loading = false
		s.units, s.unitsErr = m.units, m.err
		s.unit = 0
		for i, u := range s.units {
			if u == "matrix-"+s.cfg.Homeserver {
				s.unit = i
			}
		}
		return nil
	case spinner.TickMsg:
		if s.loading {
			var cmd tea.Cmd
			s.spin, cmd = s.spin.Update(m)
			return cmd
		}
		return nil
	case formDoneMsg:
		return s.register()
	}

	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	s.armed = false
	if k.String() == "esc" && s.mode != modeList {
		s.mode = modeList
		return nil
	}
	switch s.mode {
	case modeUser:
		// Enter on the last field sends formDoneMsg, which the app routes
		// back here (see App.Update).
		return s.userForm.Update(msg)
	case modeServices:
		switch k.String() {
		case "up", "k":
			s.unit = max(0, s.unit-1)
		case "down", "j":
			s.unit = min(len(s.units)-1, s.unit+1)
		case "enter":
			if len(s.units) > 0 {
				s.mode = modeList
				return run(deploy.Options{Action: deploy.ActionLogs, Service: s.units[s.unit]})
			}
		}
		return nil
	}

	acts := s.actions()
	switch k.String() {
	case "up", "k", "shift+tab":
		s.cursor = max(0, s.cursor-1)
	case "down", "j", "tab":
		s.cursor = min(len(acts)-1, s.cursor+1)
	case "enter", " ":
		a := acts[s.cursor]
		if a.disabled != "" {
			return nil
		}
		return s.choose(a.id)
	}
	return nil
}

func run(opts deploy.Options) tea.Cmd { return func() tea.Msg { return runActionMsg{opts} } }

func (s *manageScreen) choose(id string) tea.Cmd {
	switch id {
	case "update":
		return run(deploy.Options{Action: deploy.ActionInstall})
	case "check":
		return run(deploy.Options{Action: deploy.ActionSelfCheck})
	case "restart":
		return run(deploy.Options{Action: deploy.ActionRestart})
	case "backup":
		return run(deploy.Options{Action: deploy.ActionBackup})
	case "registration":
		s.cfg.CloseRegistration = !s.cfg.CloseRegistration
		_ = s.cfg.Save()
		return run(deploy.Options{Action: deploy.ActionInstall})
	case "setup":
		return func() tea.Msg { return gotoMsg("Network") }
	case "images":
		return run(deploy.Options{Action: deploy.ActionImages})
	case "selfupdate":
		return run(deploy.Options{Action: deploy.ActionSelfUpdate})
	case "users", "dashboard", "changes", "schedule", "restore", "rollback", "tuning", "servers", "migrate":
		screen := map[string]string{"users": "Users", "dashboard": "Dashboard", "changes": "Updates", "schedule": "Backups",
			"restore": "Restore", "rollback": "Rollback", "tuning": "Tuning", "servers": "Servers", "migrate": "Move"}[id]
		return func() tea.Msg { return gotoMsg(screen) }
	case "user":
		u := textField("name", "Username", "", "alice", "Just the name, not @alice:"+s.cfg.Domain+".")
		u.required = true
		pw := secretField("pass", "Password", config.RandomPassword(16), "Generated for you. Press ctrl+r to see it; it's shown again when the user is created.")
		pw.required = true
		s.userForm = newForm(u, pw, toggleField("admin", "Make this user a server administrator", false, ""))
		s.mode = modeUser
		return nil
	case "logs":
		s.mode = modeServices
		s.loading, s.units, s.unitsErr = true, nil, nil
		cfg := *s.cfg
		return tea.Batch(s.spin.Tick, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			units, err := deploy.ListServices(ctx, &cfg)
			return servicesMsg{units, err}
		})
	}
	return nil
}

func (s *manageScreen) register() tea.Cmd {
	f := s.userForm
	name := strings.TrimPrefix(strings.SplitN(f.value("name"), ":", 2)[0], "@")
	if name == "" || f.value("pass") == "" {
		return nil
	}
	s.mode = modeList
	return run(deploy.Options{Action: deploy.ActionRegister, User: deploy.NewUser{
		Name: strings.ToLower(name), Password: f.value("pass"), Admin: f.get("admin").on,
	}})
}

func (s *manageScreen) view(w, h int) string {
	switch s.mode {
	case modeUser:
		return s.userForm.View(w, h)
	case modeServices:
		return s.servicesView(w, h)
	}
	acts := s.actions()
	s.cursor = min(s.cursor, len(acts)-1)
	var blocks []string
	for i, a := range acts {
		cur := i == s.cursor
		bar, style := "   ", lipgloss.NewStyle().Foreground(cText)
		if cur {
			bar, style = sAccent.Render("▌")+"  ", sTitle
		}
		icon := sPink.Render("◆")
		desc := a.desc
		if a.disabled != "" {
			icon, style, desc = sFaint.Render("⊘"), sFaint, a.disabled
		}
		pad := bar + "   "
		textW := max(20, min(w, maxFormWidth)-lipgloss.Width(pad))
		var d string
		if cur && s.armed && a.disabled == "" {
			d = sWarn.Render("click again to run")
		} else if cur {
			d = sSub.Width(textW).Render(desc)
		} else {
			d = sFaint.Render(truncate(desc, textW))
		}
		blocks = append(blocks, markRow("act:"+strconv.Itoa(i), bar+icon+"  "+style.Render(a.title)+"\n"+pad+strings.ReplaceAll(d, "\n", "\n"+pad), w))
	}
	out := strings.Join(blocks, "\n\n")
	if lipgloss.Height(out) <= h {
		return out
	}
	// Too tall: one block per entry, scrolled to keep the cursor in view.
	lines := strings.Split(strings.Join(blocks, "\n"), "\n")
	top := 0
	for i := 0; i < s.cursor; i++ {
		top += lipgloss.Height(blocks[i])
	}
	curH := lipgloss.Height(blocks[s.cursor])
	start := max(0, min(top-(h-curH)/2, len(lines)-h))
	return strings.Join(lines[start:min(len(lines), start+h)], "\n")
}

func (s *manageScreen) servicesView(w, h int) string {
	if s.loading {
		return s.spin.View() + sMuted.Render(" asking the server which services exist…")
	}
	if s.unitsErr != nil {
		return sBad.Render("✗ "+s.unitsErr.Error()) + "\n\n" + sMuted.Render("Press esc to go back.")
	}
	start := max(0, min(s.unit-h/2, len(s.units)-h))
	var rows []string
	for i := start; i < len(s.units) && i < start+h; i++ {
		if i == s.unit {
			rows = append(rows, mark("unit:"+strconv.Itoa(i), sAccent.Render("▌")+"  "+sTitle.Render(s.units[i])))
		} else {
			rows = append(rows, mark("unit:"+strconv.Itoa(i), "   "+sMuted.Render(s.units[i])))
		}
	}
	return strings.Join(rows, "\n")
}
