package ui

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Maintenance ────────────────────────────────────────────────────
//
// The playbook's own maintenance, one tab each: starting, stopping and
// reinstalling services (docs/just.md, docs/playbook-tags.md), Postgres
// vacuum and upgrade (docs/maintenance-postgres.md), Synapse state
// compression (docs/maintenance-synapse.md), any playbook tags, and
// uninstalling (docs/uninstalling.md). The Postgres shell from the same doc
// needs no settings, so Manage opens it directly.

type maintTask struct{ id, label, help string }

var maintTasks = []maintTask{
	{"services", "Services", "Start, stop or reinstall every service or one group of them. Reinstalling everything also removes the components you've turned off (setup-all), which a normal update leaves in place."},
	{"disk", "Disk", "See what takes the server's disk space, remove Docker images nothing uses, or have Synapse delete media cached from other servers after a while. Media from other servers is usually what fills a small server's disk."},
	{"vacuum", "Vacuum", "Give the disk space of deleted rows back (Postgres VACUUM). Do it after deleting a lot, e.g. after compressing Synapse's state."},
	{"pgupgrade", "Upgrade", "Move the database to the newest Postgres major version: it is dumped, a new empty server started and the dump restored. The old data directory is kept until you delete it."},
	{"compress", "Compress", "Shrink Synapse's _state tables with rust-synapse-compress-state; the most effective way to reduce the database of a server in large rooms. Safe while Synapse runs, but back up first."},
	{"tags", "Tags", "Run any of the playbook's tags, as `just run-tags` does: e.g. install-mautrix-slack,start-group or ensure-matrix-users-created."},
	{"uninstall", "Uninstall", "Remove everything the playbook installed: every Matrix service, the Docker images and /matrix with all its data. Leave federated rooms first. Re-running an update almost always fixes more than reinstalling does."},
}

var vacuumPresets = []string{"vacuum-complete", "vacuum-full", "vacuum", "vacuum-analyze", "analyze"}

var vacuumLabels = []string{
	"Complete (FULL, ANALYZE; services stop)", "Full (services stop)", "Plain (services keep running)",
	"Plain + ANALYZE (services keep running)", "ANALYZE only (fastest)",
}

// remoteMediaVar is how long Synapse keeps media cached from other servers.
const remoteMediaVar = "matrix_synapse_media_retention_remote_media_lifetime"

const pgUpgradeBackup = "/matrix/postgres/data-auto-upgrade-backup"

// maintainMsg opens Maintenance on one task.
type maintainMsg string

type maintenanceScreen struct {
	cfg  *config.Config
	form *form
	task string // the tab to open on
	err  string
}

func newMaintenanceScreen() *maintenanceScreen { return &maintenanceScreen{task: "services"} }

func (s *maintenanceScreen) tool()                {}
func (s *maintenanceScreen) name() string         { return "Maintenance" }
func (s *maintenanceScreen) title() string        { return "Maintenance" }
func (s *maintenanceScreen) typing() bool         { return s.form.typing() }
func (s *maintenanceScreen) leave(*config.Config) {}
func (s *maintenanceScreen) activeForm() *form    { return s.form }
func (s *maintenanceScreen) keys() [][2]string {
	return [][2]string{{"←→", "task"}, {"↑↓", "move"}, {"enter", "run"}}
}

// available drops the tasks this server can't do.
func (s *maintenanceScreen) available() []maintTask {
	ext := s.cfg.Components["external-postgres"]
	return slices.DeleteFunc(slices.Clone(maintTasks), func(t maintTask) bool {
		switch t.id {
		case "vacuum", "pgupgrade":
			return ext
		case "compress":
			return ext || s.cfg.Homeserver != "synapse"
		}
		return false
	})
}

func (s *maintenanceScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.err = c, ""
	diskIDs, diskLabels := []string{"usage", "prune"}, []string{"Show what uses the disk", "Remove unused Docker data"}
	if c.Homeserver == "synapse" {
		diskIDs, diskLabels = append(diskIDs, "retention"), append(diskLabels, "Delete old media from other servers")
	}
	var ids, labels []string
	for _, t := range s.available() {
		ids, labels = append(ids, t.id), append(labels, t.label)
	}
	if !slices.Contains(ids, s.task) {
		s.task = ids[0]
	}
	f := newForm(
		choiceField("task", "Task", ids, labels, s.task, ""),
		textField("group", "Service group", "", "everything",
			"Empty for every service, or one group: synapse, postgres, traefik, element, mautrix-telegram, bots, bridges…"),
		choiceField("do", "Do", []string{"start", "stop", "reinstall"}, []string{"Start or restart", "Stop", "Reinstall"}, "start", ""),
		choiceField("disk", "Do", diskIDs, diskLabels, diskIDs[0], ""),
		textField("keep", "Keep media from other servers for (days)", strings.TrimSuffix(c.Overrides[remoteMediaVar], "d"), "forever",
			"Empty keeps it forever. Synapse fetches it again if someone opens it later; your own users' uploads are never deleted."),
		choiceField("preset", "Kind of vacuum", vacuumPresets, vacuumLabels, vacuumPresets[0],
			"The FULL kinds need about as much free disk space as the database takes, and stop every service while they run."),
		textField("backup", "Keep the old data in", pgUpgradeBackup, pgUpgradeBackup, "Stays until you delete it."),
		textField("min", "Compress rooms with more state groups than", "100000", "100000", ""),
		textField("tags", "Tags", "", "install-mautrix-slack,start-group", "Comma-separated."),
		textField("vars", "Extra vars", "", "group=mautrix-slack", "key=value pairs separated by spaces, passed as --extra-vars."),
		textField("confirm", "Type "+c.Domain+" to confirm", "", c.Domain, ""),
	)
	on := func(ids ...string) func() bool {
		return func() bool { return !slices.Contains(ids, f.value("task")) }
	}
	f.get("group").hidden, f.get("do").hidden = on("services"), on("services")
	f.get("disk").hidden = on("disk")
	f.get("keep").hidden = func() bool { return f.value("task") != "disk" || f.value("disk") != "retention" }
	f.get("preset").hidden = on("vacuum")
	f.get("backup").hidden = on("pgupgrade")
	f.get("min").hidden = on("compress")
	f.get("tags").hidden, f.get("vars").hidden = on("tags"), on("tags")
	f.get("confirm").hidden = on("uninstall")
	s.form = f
	s.explain()
	return nil
}

func (s *maintenanceScreen) explain() {
	for _, t := range maintTasks {
		if t.id == s.form.value("task") {
			s.form.get("task").help = t.help
		}
	}
}

// open shows task, from Manage's list; enter builds the form on it, or
// it switches the form already open.
func (s *maintenanceScreen) open(task string) {
	s.task, s.err = task, ""
	if s.form == nil {
		return
	}
	f := s.form.get("task")
	if i := slices.Index(f.choices, task); i >= 0 {
		f.choice = i
		s.explain()
	}
}

func (s *maintenanceScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(formDoneMsg); ok {
		return s.run()
	}
	cmd := s.form.Update(msg)
	s.task = s.form.value("task")
	s.explain()
	if _, ok := msg.(tea.KeyMsg); ok {
		s.err = ""
	}
	return cmd
}

// options turns the form into the run it asks for.
func (s *maintenanceScreen) options() (deploy.Options, string) {
	f := s.form
	o := deploy.Options{Action: deploy.ActionTags}
	switch f.value("task") {
	case "services":
		g, do := strings.TrimSpace(f.value("group")), f.value("do")
		if g == "" || g == "everything" {
			o.Tags = map[string]string{"start": "start-all", "stop": "stop-all", "reinstall": "setup-all,ensure-matrix-users-created,start"}[do]
			o.Title = map[string]string{"start": "Restart every service", "stop": "Stop every service", "reinstall": "Reinstall everything"}[do]
			return o, ""
		}
		if known := deploy.ServiceGroups(s.cfg.PlaybookDir); len(known) > 0 && !slices.Contains(known, g) {
			return o, "The playbook has no service group " + strconv.Quote(g) + "."
		}
		o.Vars = map[string]string{"group": g}
		o.Tags = map[string]string{"start": "start-group", "stop": "stop-group", "reinstall": "install-" + g + ",start-group"}[do]
		o.Title = map[string]string{"start": "Restart ", "stop": "Stop ", "reinstall": "Reinstall "}[do] + g
	case "disk":
		switch f.value("disk") {
		case "usage":
			o.Action = deploy.ActionDisk
		case "prune":
			o.Tags, o.Title = "run-docker-prune", "Remove unused Docker data"
		case "retention":
			if _, err := s.keepDays(); err != nil {
				return o, err.Error()
			}
			o.Tags, o.Title = "install-synapse,start-group", "Apply Synapse's media retention"
			o.Vars = map[string]string{"group": "synapse"}
		}
	case "vacuum":
		o.Tags, o.Title = "run-postgres-vacuum", "Vacuum the database"
		o.Vars = map[string]string{"postgres_vacuum_preset": f.value("preset")}
	case "pgupgrade":
		o.Tags, o.Title = "upgrade-postgres", "Upgrade Postgres"
		o.Vars = map[string]string{"postgres_auto_upgrade_backup_data_path": f.value("backup")}
	case "compress":
		n, err := strconv.Atoi(f.value("min"))
		if err != nil || n < 1 {
			return o, "The number of state groups must be a whole number."
		}
		o.Tags, o.Title = "rust-synapse-compress-state", "Compress Synapse's state"
		o.Vars = map[string]string{"matrix_synapse_rust_synapse_compress_state_min_state_groups_required": strconv.Itoa(n)}
	case "tags":
		o.Tags, o.Title = f.value("tags"), "Run "+f.value("tags")
		if o.Tags == "" {
			return o, "Enter the tags to run."
		}
		for _, kv := range strings.Fields(f.value("vars")) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return o, strconv.Quote(kv) + " isn't key=value."
			}
			if o.Vars == nil {
				o.Vars = map[string]string{}
			}
			o.Vars[k] = v
		}
	case "uninstall":
		if f.value("confirm") != s.cfg.Domain {
			return o, "Type " + s.cfg.Domain + " to confirm."
		}
		o.Action = deploy.ActionUninstall
	}
	return o, ""
}

// keepDays is the retention typed in, 0 for forever.
func (s *maintenanceScreen) keepDays() (int, error) {
	v := strings.TrimSpace(s.form.value("keep"))
	if v == "" || v == "forever" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, errors.New("The days to keep media for must be a whole number, or empty for forever.")
	}
	return n, nil
}

// databaseShell runs psql on the server in this terminal, the TUI paused.
func databaseShell(c *config.Config) tea.Cmd {
	return tea.ExecProcess(deploy.DatabaseShell(c), func(err error) tea.Msg {
		if err != nil {
			return noteMsg("The database shell ended: " + err.Error())
		}
		return nil
	})
}

func (s *maintenanceScreen) run() tea.Cmd {
	o, problem := s.options()
	if s.err = problem; problem != "" {
		return nil
	}
	if o.Action == deploy.ActionUninstall {
		s.form.get("confirm").input.SetValue("")
	}
	if s.form.value("task") == "disk" && s.form.value("disk") == "retention" {
		// Saved like a Tuning change, so later deploys keep it.
		if days, _ := s.keepDays(); days == 0 {
			delete(s.cfg.Overrides, remoteMediaVar)
		} else {
			s.cfg.Overrides[remoteMediaVar] = strconv.Itoa(days) + "d"
		}
		_ = s.cfg.Save()
	}
	return run(o)
}

func (s *maintenanceScreen) view(w, h int) string {
	out := s.form.View(w, h-2)
	if s.err != "" {
		out += "\n\n" + sBad.Render("✗ "+s.err)
	}
	return out
}
