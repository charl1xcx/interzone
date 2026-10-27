package ui

import (
	"context"
	"sort"
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
	modeLogs
)

type manageAction struct {
	id, title, desc string
	disabled        string // why it can't be used, if it can't
}

// manageGroups are the headings Manage lists its actions under, and
// actionGroup and actionIcons place and mark each action.
var manageGroups = []string{"Keep it running", "People", "Data", "Look closer", "Change things"}

var actionGroup = map[string]int{
	"update": 0, "check": 0, "restart": 0, "services": 0, "changes": 0, "images": 0, "selfupdate": 0,
	"user": 1, "registration": 1, "users": 1, "tokens": 1,
	"backup": 2, "schedule": 2, "restore": 2, "rollback": 2, "vacuum": 2, "pgupgrade": 2, "compress": 2,
	"dashboard": 3, "logs": 3, "tuning": 3, "psql": 3, "tags": 3,
	"setup": 4, "migrate": 4, "servers": 4, "uninstall": 4,
}

var actionIcons = map[string]string{
	"update": "↻", "check": "✓", "restart": "↯", "changes": "±", "images": "▣", "selfupdate": "⇪",
	"user": "+", "registration": "⊕", "users": "@", "tokens": "⚿",
	"backup": "⇩", "schedule": "◷", "restore": "↺", "rollback": "↶",
	"dashboard": "▦", "logs": "≡", "tuning": "✎",
	"setup": "◇", "migrate": "⇢", "servers": "▤",
	"services": "⏻", "vacuum": "⌫", "pgupgrade": "⇡", "compress": "⇲", "psql": "$", "tags": "#", "uninstall": "✗",
}

// runsAtOnce are the Manage actions that start changing the server as
// soon as they're chosen; a click on one needs a second click.
var runsAtOnce = map[string]bool{"update": true, "check": true, "restart": true, "backup": true,
	"registration": true, "images": true, "selfupdate": true}

// hotkeys run Manage actions from the list with one key; the runsAtOnce
// ones need it pressed twice.
var hotkeys = map[string]string{"update": "u", "check": "c", "restart": "r", "user": "a", "registration": "g",
	"users": "m", "tokens": "t", "dashboard": "d", "changes": "n", "images": "i", "backup": "b", "schedule": "s",
	"restore": "e", "rollback": "z", "logs": "l", "tuning": "f", "setup": "w", "migrate": "v", "servers": "o",
	"services": "p", "tags": "x"}

type manageScreen struct {
	cfg      *config.Config
	cursor   int
	armed    bool   // a click or hotkey selected a runsAtOnce action; doing it again runs it
	armedKey string // the hotkey that armed it, or "" for a click
	mode     manageMode

	userForm *form

	loading  bool
	units    []string
	unitsErr error
	unit     int
	spin     spinner.Model

	logs liveLogs
}

func newManageScreen() *manageScreen {
	return &manageScreen{spin: newSpinner()}
}

func (s *manageScreen) name() string { return "Manage" }
func (s *manageScreen) title() string {
	switch s.mode {
	case modeUser:
		return "Add a user"
	case modeServices:
		return "Which service's logs?"
	case modeLogs:
		return "Logs"
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
	case modeLogs:
		return [][2]string{{"↑↓", "scroll"}, {"end", "follow"}}
	}
	return [][2]string{{"↑↓", "select"}, {"enter", "run"}, {"a–z", "shortcuts"}}
}

// inSubview reports whether esc should return to the action list rather
// than leave the screen.
func (s *manageScreen) inSubview() bool { return s.mode != modeList }

func (s *manageScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.logs.stop()
	s.mode, s.armed = modeList, false
	return nil
}

func (s *manageScreen) leave(*config.Config) { s.logs.stop() }

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
	switch c.Homeserver {
	case "conduit", "continuwuity", "tuwunel":
		if c.RegistrationOpen() {
			acts = append(acts, manageAction{id: "registration", title: "Close registration",
				desc: "Stop new sign-ups now that your account exists. Re-runs the install to apply it."})
		} else {
			acts = append(acts, manageAction{id: "registration", title: "Open registration",
				desc: "Allow sign-ups again (with the registration token where supported). Re-runs the install to apply it."})
		}
	case "synapse":
		if !deploy.UsesMAS(c) {
			break
		}
		if c.RegistrationOpen() {
			acts = append(acts, manageAction{id: "registration", title: "Close registration",
				desc: "Anyone with the token " + c.SignupToken + " can sign up. Close it to stop new accounts; existing ones keep working. Re-runs the install to apply it."})
		} else {
			acts = append(acts, manageAction{id: "registration", title: "Open registration",
				desc: "Let anyone with a sign-up token create an account through MAS, without an email address. Re-runs the install to apply it."})
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
			desc: "Follow the log of any Matrix service as it's written."},
		manageAction{id: "tuning", title: "Fine-tune the roles",
			desc: "Browse and change the default variables of every playbook role: " + homeserverName(c.Homeserver) + ", bridges, Traefik, Postgres and the rest. Applied on the next update."},
		manageAction{id: "setup", title: "Change the setup",
			desc: "Go through the wizard again: components, mirrors, certificates. Saved answers are kept."},
		manageAction{id: "migrate", title: "Move to another server",
			desc: "Copy everything in /matrix to a new machine and install there; the old one is stopped."},
		manageAction{id: "servers", title: "Other servers",
			desc: "Switch between the servers interzone manages, or add another."},
		manageAction{id: "services", title: "Start, stop or reinstall services",
			desc: "Every service or one group of them. Reinstalling everything also removes the components you've turned off."},
		manageAction{id: "vacuum", title: "Vacuum the database",
			desc: "Give the disk space of deleted rows back to the server (Postgres VACUUM)."},
		manageAction{id: "pgupgrade", title: "Upgrade Postgres",
			desc: "Move the database to the newest Postgres major version; the playbook never does it by itself."},
		manageAction{id: "compress", title: "Compress Synapse's state",
			desc: "Shrink Synapse's largest tables with rust-synapse-compress-state."},
		manageAction{id: "psql", title: "Database shell",
			desc: "Open psql on the server's Postgres, right here in the terminal."},
		manageAction{id: "tags", title: "Run playbook tags",
			desc: "Run any of the playbook's tags with extra vars, as just run-tags does."},
		manageAction{id: "uninstall", title: "Uninstall everything",
			desc: "Remove every Matrix service, the Docker images and /matrix with all its data from the server."},
		manageAction{id: "selfupdate", title: "Update interzone",
			desc: "Download the newest interzone release and replace this program with it."},
	)
	if deploy.UsesMAS(c) {
		acts = append(acts, manageAction{id: "tokens", title: "Sign-up tokens",
			desc: "See how each registration token has been used, make new ones that run out after some uses or days, and revoke them."})
	}
	for i := range acts {
		switch {
		case acts[i].id == "user" && c.Homeserver != "synapse" && c.Homeserver != "dendrite":
			acts[i].disabled = "Only Synapse and Dendrite can register users from Ansible; use a registration token or open registration instead."
		case acts[i].id == "users" && c.Homeserver != "synapse":
			acts[i].disabled = "Needs Synapse's admin API; " + homeserverName(c.Homeserver) + " doesn't have one."
		case acts[i].id == "tokens" && c.Components["external-postgres"]:
			acts[i].disabled = "MAS's tokens are read from the bundled Postgres; you use an external one."
		case acts[i].id == "compress" && c.Homeserver != "synapse":
			acts[i].disabled = "Only Synapse has state to compress."
		case c.Components["external-postgres"] && (acts[i].id == "vacuum" || acts[i].id == "pgupgrade" || acts[i].id == "compress" || acts[i].id == "psql"):
			acts[i].disabled = "You use an external Postgres server; maintain it where it runs."
		case c.Components["external-postgres"] && (acts[i].id == "backup" || acts[i].id == "schedule" || acts[i].id == "restore"):
			acts[i].disabled = "You use an external Postgres server; back it up where it runs."
		}
	}
	sort.SliceStable(acts, func(i, j int) bool { return actionGroup[acts[i].id] < actionGroup[acts[j].id] })
	return acts
}

func (s *manageScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case servicesMsg:
		s.loading = false
		s.unitsErr = m.err
		if m.err != nil {
			return nil // a list loaded before stays
		}
		saveCache("services", m.units)
		cur := ""
		if s.unit < len(s.units) {
			cur = s.units[s.unit]
		}
		s.units, s.unit = m.units, 0
		for i, u := range s.units {
			if u == cur || cur == "" && u == "matrix-"+s.cfg.Homeserver {
				s.unit = i
			}
		}
		return nil
	case logLinesMsg:
		return s.logs.add(m)
	case spinner.TickMsg:
		return spinning(s.loading, &s.spin, m)
	case formDoneMsg:
		return s.register()
	}

	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	wasArmed := s.armed && s.armedKey == k.String()
	s.armed, s.armedKey = false, ""
	if k.String() == "esc" && s.mode == modeLogs {
		s.logs.stop()
		s.mode = modeServices
		return nil
	}
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
				s.mode = modeLogs
				return s.logs.start(s.cfg, s.units[s.unit])
			}
		}
		return nil
	case modeLogs:
		s.logs.key(k.String())
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
	for i, a := range acts {
		if hotkeys[a.id] != k.String() || a.disabled != "" {
			continue
		}
		if runsAtOnce[a.id] && !(wasArmed && s.cursor == i) {
			s.cursor, s.armed, s.armedKey = i, true, k.String()
			return nil
		}
		s.cursor = i
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
		if s.cfg.Homeserver == "synapse" {
			s.cfg.Signup = !s.cfg.Signup
			s.cfg.FillGenerated()
		} else {
			s.cfg.CloseRegistration = !s.cfg.CloseRegistration
		}
		_ = s.cfg.Save()
		return run(deploy.Options{Action: deploy.ActionInstall})
	case "setup":
		return func() tea.Msg { return gotoMsg("Server") }
	case "images":
		return run(deploy.Options{Action: deploy.ActionImages})
	case "selfupdate":
		return run(deploy.Options{Action: deploy.ActionSelfUpdate})
	case "users", "tokens", "dashboard", "changes", "schedule", "restore", "rollback", "tuning", "servers", "migrate":
		screen := map[string]string{"users": "Users", "tokens": "Tokens", "dashboard": "Dashboard", "changes": "Updates", "schedule": "Backups",
			"restore": "Restore", "rollback": "Rollback", "tuning": "Tuning", "servers": "Servers", "migrate": "Move"}[id]
		return func() tea.Msg { return gotoMsg(screen) }
	case "psql":
		return databaseShell(s.cfg)
	case "services", "vacuum", "pgupgrade", "compress", "tags", "uninstall":
		return func() tea.Msg { return maintainMsg(id) }
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
		if units, _, ok := loadCache[[]string]("services"); ok {
			s.units, s.unit = units, 0 // shown while the fresh list loads
			for i, u := range units {
				if u == "matrix-"+s.cfg.Homeserver {
					s.unit = i
				}
			}
		}
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
	case modeLogs:
		return s.logs.view(w, h)
	}
	acts := s.actions()
	s.cursor = min(s.cursor, len(acts)-1)
	tw := min(w, maxFormWidth)
	var lines []string
	cur := 0 // the line the cursor's entry starts on
	for i, a := range acts {
		if g := actionGroup[a.id]; i == 0 || g != actionGroup[acts[i-1].id] {
			if i > 0 && !tight {
				lines = append(lines, "")
			}
			lines = append(lines, rule(manageGroups[g], tw))
		}
		icon, style := sPink.Render(actionIcons[a.id]), lipgloss.NewStyle().Foreground(cText)
		if a.disabled != "" {
			icon, style = sFaint.Render("⊘"), sFaint
		}
		hot := ""
		if k := hotkeys[a.id]; k != "" && a.disabled == "" {
			hot = keyChip(k)
		}
		id := "act:" + strconv.Itoa(i)
		if i != s.cursor {
			row := "  " + icon + "  " + style.Render(truncate(a.title, tw-12))
			if hot != "" {
				row += strings.Repeat(" ", max(1, tw-lipgloss.Width(row)-lipgloss.Width(hot)-2)) + hot
			}
			lines = append(lines, markRow(id, row, w))
			continue
		}
		cur = len(lines)
		inner := tw - 4
		head := icon + "  " + sTitle.Render(truncate(a.title, inner-8))
		if hot != "" {
			head += strings.Repeat(" ", max(1, inner-lipgloss.Width(head)-lipgloss.Width(hot))) + hot
		}
		desc := a.desc
		if a.disabled != "" {
			desc = a.disabled
		}
		var body []string
		switch {
		case s.armed && a.disabled == "" && s.armedKey != "":
			body = []string{sWarn.Render("press " + s.armedKey + " again to run")}
		case s.armed && a.disabled == "":
			body = []string{sWarn.Render("click again to run")}
		case tight:
			body = []string{sSub.Render(truncate(desc, inner-3))}
		default:
			body = strings.Split(sSub.Width(inner-3).Render(desc), "\n")
		}
		for j := range body { // under the title, past the icon
			body[j] = "   " + body[j]
		}
		lines = append(lines, markLines(id, picked(append([]string{head}, body...), tw)))
	}
	out := strings.Join(lines, "\n")
	if lipgloss.Height(out) <= h {
		return out
	}
	return windowAround(out, cur, h)
}

func (s *manageScreen) servicesView(w, h int) string {
	if s.loading && len(s.units) == 0 {
		return s.spin.View() + sMuted.Render(" asking the server which services exist…")
	}
	if s.unitsErr != nil && len(s.units) == 0 {
		return sBad.Render("✗ "+s.unitsErr.Error()) + "\n\n" + sMuted.Render("Press esc to go back.")
	}
	head := ""
	switch {
	case s.loading:
		head = s.spin.View() + sFaint.Render(" refreshing…") + "\n\n"
	case s.unitsErr != nil:
		head = sBad.Render(truncate("✗ "+s.unitsErr.Error(), w)) + "\n\n"
	}
	h -= lipgloss.Height(head)
	start := max(0, min(s.unit-h/2, len(s.units)-h))
	var rows []string
	for i := start; i < len(s.units) && i < start+h; i++ {
		if i == s.unit {
			rows = append(rows, mark("unit:"+strconv.Itoa(i), sAccent.Render("▌")+"  "+sTitle.Render(s.units[i])))
		} else {
			rows = append(rows, mark("unit:"+strconv.Itoa(i), "   "+sMuted.Render(s.units[i])))
		}
	}
	return head + strings.Join(rows, "\n")
}
