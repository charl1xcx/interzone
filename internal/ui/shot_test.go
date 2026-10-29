package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"interzone/internal/catalog"
	"interzone/internal/config"
	"interzone/internal/deploy"
)

// TestShot draws screens that need a server, on the layout tests' sample
// data and in colour, as .ans files for scripts/screenshots/shots.sh. It
// runs only when INTERZONE_SHOT names the directory to write them to.
func TestShot(t *testing.T) {
	dir := os.Getenv("INTERZONE_SHOT")
	if dir == "" {
		t.Skip("INTERZONE_SHOT is not set")
	}
	shots := []struct {
		name string
		open func(*App)
	}{
		{"users", openUsers},
		// The wiki's guide, on the Friends & family preset.
		{"wiki-confirm", func(a *App) {
			a.cfg.Target = config.Target{Mode: "ssh", Host: "192.0.2.1", User: "root"}
			a.jump(a.index("Review"))
			rs := a.cur().(*reviewScreen)
			rs.checking, rs.verbose, rs.confirm = false, true, true
			rs.checks = []deploy.Finding{
				{Group: "Server", Name: "Connect", Detail: "root@192.0.2.1, Debian 12, 4 CPUs, 8 GB RAM"},
				{Group: "DNS", Name: "matrix.example.org", Service: "Synapse", Detail: "192.0.2.1"},
				{Group: "DNS", Name: "element.example.org", Service: "Element Web", Detail: "192.0.2.1"},
				{Group: "DNS", Name: "sable.example.org", Service: "Sable", Detail: "192.0.2.1"},
				{Group: "DNS", Name: "call.element.example.org", Service: "Element Call", Detail: "192.0.2.1"},
				{Group: "Federation", Name: "example.org", Detail: ".well-known/matrix/server points at matrix.example.org:443"},
			}
		}},
		{"wiki-deploy", func(a *App) { fakeDeploy(a, false) }},
		{"wiki-deployed", func(a *App) { fakeDeploy(a, true) }},
		{"wiki-manage", func(a *App) {
			a.jump(a.index("Manage"))
			a.health = &deploy.Health{Up: true, Trusted: true, CertLeft: 89 * 24 * time.Hour}
		}},
		{"wiki-dashboard", openDashboard},
		{"wiki-backups", func(a *App) {
			a.jump(a.index("Backups"))
			a.cur().(*backupsScreen).form.get("on").on = true
		}},
		{"wiki-alerts", func(a *App) {
			a.jump(a.index("Alerts"))
			f := a.cur().(*alertsScreen).form
			f.get("on").on = true
			f.get("format").choice = 2
			f.get("room").input.SetValue("#alerts:example.org")
		}},
	}
	// The wiki's component pages: each category's tab, and its settings.
	for i, ids := range [][]string{
		{"coturn", "exim", "ddclient"},
		{"element", "cinny", "sable", "fluffychat"},
		{"mas", "ketesa", "shared-secret-auth", "ldap-auth"},
		{"s3-provider"},
		{"double-puppet", "bridges-e2ee", "telegram", "whatsapp", "signal", "postmoogle"},
		{"reminder-bot", "honoroit", "baibot", "draupnir"},
		{"monitoring", "alertmanager-receiver", "borg"},
		{"element-call", "jitsi", "etherpad", "ntfy", "sygnal"},
	} {
		slug := strings.ToLower(strings.NewReplacer(" & ", "-", " ", "-").Replace(catalog.Categories[i]))
		tick := func(a *App) {
			a.cfg.Components = map[string]bool{"element": true, "mas": true}
			for _, id := range ids {
				a.cfg.Components[id] = true
			}
		}
		shots = append(shots, struct {
			name string
			open func(*App)
		}{"wiki-comp-" + slug, func(a *App) {
			tick(a)
			a.jump(a.index("Components"))
			a.cur().(*componentsScreen).cat = i
		}}, struct {
			name string
			open func(*App)
		}{"wiki-set-" + slug, func(a *App) {
			tick(a)
			a.jump(a.index("Settings"))
			f := a.cur().(*settingsScreen).form
			for j, fl := range f.fields { // the category's first field, or else its first address
				if strings.HasPrefix(fl.key, ids[0]+".") || fl.key == "host."+ids[0] {
					f.focusOn(j)
					break
				}
			}
		}})
	}
	for _, s := range shots {
		a := demoApp(t, 120, 36)
		a.cfg.Sources.Blackout = true // like the other shots
		if strings.HasPrefix(s.name, "wiki-") {
			t.Setenv("XDG_CONFIG_HOME", "/home/demo/.config")
			a.cfg.Sources.Blackout = false
			a.cfg.Components = map[string]bool{}
			for _, id := range catalog.Presets[1].Components {
				a.cfg.Components[id] = true
			}
		}
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(true)
		s.open(a)
		if strings.HasPrefix(s.name, "wiki-") {
			a.status = "" // the demo's home can't be saved to
		}
		if err := os.WriteFile(filepath.Join(dir, s.name+".ans"), []byte(a.View()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
}

// fakeDeploy shows a first install part way through, or finished, without
// starting one: entering the Deploy screen would.
func fakeDeploy(a *App, finished bool) {
	a.step = a.index("Deploy")
	ds := a.cur().(*deployScreen)
	ds.cfg, ds.opts = a.cfg, deploy.Options{Action: deploy.ActionInstall}
	ds.states, ds.running, ds.follow = map[deploy.Step]stepState{}, true, true
	ds.started = time.Now().Add(-14 * time.Minute)
	ds.handle(deploy.Event{Plan: deploy.Plan(deploy.ActionInstall)})
	for _, st := range []deploy.Step{deploy.StepAnsible, deploy.StepPlaybook, deploy.StepInventory, deploy.StepRoles, deploy.StepImages, deploy.StepMirrors, deploy.StepInstall} {
		ds.handle(deploy.Event{Step: st, Started: true})
	}
	for _, task := range []string{"matrix-base : Ensure Matrix base paths exist", "docker : Install Docker", "matrix-postgres : Ensure Postgres container is running",
		"matrix-synapse : Ensure Synapse container image is pulled", "matrix-client-element : Ensure Element Web config installed", "coturn : Ensure Coturn configuration is in place"} {
		line := "TASK [" + task + "] ****"
		ds.handle(deploy.Event{Step: deploy.StepInstall, Line: line, Task: task})
		ds.handle(deploy.Event{Step: deploy.StepInstall, Line: "ok: [matrix.example.org]"})
	}
	ds.elapsed = 14*time.Minute + 32*time.Second
	if finished {
		ds.handle(deploy.Event{Step: deploy.StepSelfCheck, Started: true})
		ds.handle(deploy.Event{Done: true, Result: &deploy.Result{Passed: []string{"Synapse", "Element Web", "Sable", "Element Call", "Matrix Authentication Service", "Ketesa"}}})
		ds.elapsed = 23*time.Minute + 8*time.Second
	}
}
