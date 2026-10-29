package ui

import (
	"maps"
	"testing"

	"interzone/internal/deploy"
)

// Manage opens Maintenance on the chosen task, and each task asks for the
// playbook run its doc describes.
func TestMaintenance(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Manage"))
	press(a, runes("x")) // Run playbook tags
	s, ok := a.cur().(*maintenanceScreen)
	if !ok || s.form.value("task") != "tags" {
		t.Fatalf("on %s", a.cur().name())
	}

	set := func(task string, values map[string]string) {
		deliver(a, maintainMsg(task))
		for k, v := range values {
			if k == "do" || k == "preset" || k == "disk" {
				f := s.form.get(k)
				for i, c := range f.choices {
					if c == v {
						f.choice = i
					}
				}
				continue
			}
			s.form.get(k).input.SetValue(v)
		}
	}
	for _, tc := range []struct {
		task   string
		values map[string]string
		action deploy.Action
		tags   string
		vars   map[string]string
		err    bool
	}{
		{"services", map[string]string{"do": "reinstall"}, deploy.ActionTags, "setup-all,ensure-matrix-users-created,start", nil, false},
		{"services", map[string]string{"group": "postgres", "do": "stop"}, deploy.ActionTags, "stop-group", map[string]string{"group": "postgres"}, false},
		{"services", map[string]string{"group": "mautrix-slack", "do": "reinstall"}, deploy.ActionTags, "install-mautrix-slack,start-group", map[string]string{"group": "mautrix-slack"}, false},
		{"disk", map[string]string{"disk": "usage"}, deploy.ActionDisk, "", nil, false},
		{"disk", map[string]string{"disk": "prune"}, deploy.ActionTags, "run-docker-prune", nil, false},
		{"disk", map[string]string{"disk": "retention", "keep": "30"}, deploy.ActionTags, "install-synapse,start-group", map[string]string{"group": "synapse"}, false},
		{"disk", map[string]string{"disk": "retention", "keep": "a month"}, 0, "", nil, true},
		{"vacuum", map[string]string{"preset": "analyze"}, deploy.ActionTags, "run-postgres-vacuum", map[string]string{"postgres_vacuum_preset": "analyze"}, false},
		{"pgupgrade", nil, deploy.ActionTags, "upgrade-postgres", map[string]string{"postgres_auto_upgrade_backup_data_path": pgUpgradeBackup}, false},
		{"compress", map[string]string{"min": "5000"}, deploy.ActionTags, "rust-synapse-compress-state", map[string]string{"matrix_synapse_rust_synapse_compress_state_min_state_groups_required": "5000"}, false},
		{"compress", map[string]string{"min": "lots"}, 0, "", nil, true},
		{"tags", map[string]string{"tags": "install-mautrix-slack,start-group", "vars": "group=mautrix-slack"}, deploy.ActionTags, "install-mautrix-slack,start-group", map[string]string{"group": "mautrix-slack"}, false},
		{"tags", map[string]string{"tags": "x", "vars": "oops"}, 0, "", nil, true},
		{"uninstall", map[string]string{"confirm": "wrong.org"}, 0, "", nil, true},
		{"uninstall", map[string]string{"confirm": a.cfg.Domain}, deploy.ActionUninstall, "", nil, false},
	} {
		set(tc.task, tc.values)
		o, problem := s.options()
		if tc.err {
			if problem == "" {
				t.Errorf("%s %v: no problem reported", tc.task, tc.values)
			}
			continue
		}
		if problem != "" || o.Action != tc.action || o.Tags != tc.tags || !maps.Equal(o.Vars, tc.vars) {
			t.Errorf("%s %v: got %v %q %v (%s)", tc.task, tc.values, o.Action, o.Tags, o.Vars, problem)
		}
	}

	// Applying a retention keeps it for later deploys; empty keeps media forever.
	set("disk", map[string]string{"disk": "retention", "keep": "14"})
	deliver(a, formDoneMsg{})
	if got := a.cfg.Overrides[remoteMediaVar]; got != "14d" {
		t.Errorf("retention saved as %q", got)
	}
	a.jump(a.index("Maintenance"))
	set("disk", map[string]string{"disk": "retention", "keep": ""})
	deliver(a, formDoneMsg{})
	if _, ok := a.cfg.Overrides[remoteMediaVar]; ok {
		t.Errorf("forever left the retention set")
	}
	a.jump(a.index("Maintenance"))

	// Enter with a problem stays on the screen and says so.
	set("uninstall", map[string]string{"confirm": "nope"})
	deliver(a, formDoneMsg{})
	if a.cur() != s || s.err == "" {
		t.Errorf("an unconfirmed uninstall ran")
	}
}
