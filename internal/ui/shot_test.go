package ui

import (
	"net/netip"
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
		{"dashboard", openDashboard},
		{"host-map", openSwarm},
		// The wiki's guide, on the Friends & family preset.
		{"wiki-confirm", openConfirm},
		{"wiki-deploy", func(a *App) { fakeDeploy(a, false) }},
		{"wiki-deployed", func(a *App) { fakeDeploy(a, true) }},
		{"wiki-manage", openManage},
		{"wiki-dashboard", openDashboard},
		{"wiki-backups", openBackups},
		{"wiki-alerts", openAlerts},
	}
	// The wiki's component pages: each category's tab, and its settings.
	for i, ids := range [][]string{
		{"coturn", "exim", "ddclient"},
		{"element", "cinny", "sable", "fluffychat"},
		{"uvs", "mas", "ketesa", "element-admin"},
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
	// The wiki's gallery: a tour of the app, drawn 142x34 to fit 1600x900.
	for _, g := range []struct {
		name string
		open func(*App)
	}{
		{"welcome", func(a *App) {}},
		{"server", func(a *App) { a.jump(a.index("Server")) }},
		{"homeserver", func(a *App) { a.jump(a.index("Homeserver")) }},
		{"components", func(a *App) { a.jump(a.index("Components")) }},
		{"settings", func(a *App) { a.jump(a.index("Settings")) }},
		{"review", openConfirm},
		{"deploy", func(a *App) { fakeDeploy(a, false) }},
		{"deployed", func(a *App) { fakeDeploy(a, true) }},
		{"manage", openManage},
		{"keys", func(a *App) { openManage(a); a.sheetOpen = true }},
		{"dashboard", openDashboard},
		{"users", openUsers},
		{"rooms", openRooms},
		{"tokens", openTokens},
		{"updates", func(a *App) {
			a.jump(a.index("Updates"))
			deliver(a, changesMsg{ch: &deploy.Changes{From: "7dbe652020aa", To: "86ebd3b5b5cc", Total: 23,
				Notices: []deploy.Notice{
					{Date: "2026-10-21", Title: "Synapse now needs PostgreSQL 15 or newer", Body: "Upgrade Postgres before updating: Manage › Upgrade Postgres does it for you.", Yours: []string{"Synapse"}},
					{Date: "2026-10-12", Title: "Jitsi moves to its stable-10314 release"},
				},
				Versions: []deploy.VersionChange{
					{Name: "matrix_synapse_version", Old: "v1.139.2", New: "v1.140.0"},
					{Name: "matrix_client_element_version", Old: "v1.12.1", New: "v1.12.3"},
					{Name: "matrix_element_call_version", Old: "v0.16.1", New: "v0.17.0"},
					{Name: "matrix_ketesa_version", New: "v0.4.0"},
				},
				Commits: []deploy.Commit{
					{Hash: "86ebd3b5b5", Date: "2026-10-28", Subject: "Upgrade Synapse (v1.139.2 -> v1.140.0)"},
					{Hash: "41c9e07a12", Date: "2026-10-27", Subject: "Upgrade Element Web (v1.12.1 -> v1.12.3)"},
					{Hash: "b03e5f8d44", Date: "2026-10-25", Subject: "Upgrade Element Call (v0.16.1 -> v0.17.0)"},
					{Hash: "9a7d21c6e0", Date: "2026-10-24", Subject: "Add Ketesa admin panel role"},
				}}})
		}},
		{"rollback", func(a *App) {
			a.jump(a.index("Rollback"))
			day := time.Date(2026, 10, 28, 4, 12, 0, 0, time.UTC)
			a.cur().(*historyScreen).list = []deploy.Deploy{
				{Time: day, Commit: "86ebd3b5b5cc", Subject: "Upgrade Synapse (v1.139.2 -> v1.140.0)", Version: "v0.5.43", Domain: "example.org", Homeserver: "synapse", Components: 6},
				{Time: day.AddDate(0, 0, -9), Commit: "7dbe652020aa", Subject: "Upgrade Element Web (v1.12.0 -> v1.12.1)", Version: "v0.5.41", Domain: "example.org", Homeserver: "synapse", Components: 6},
				{Time: day.AddDate(0, 0, -30), Commit: "c4f19a0b77de", Subject: "Upgrade Coturn (4.6.3 -> 4.7.0)", Version: "v0.5.36", Domain: "example.org", Homeserver: "synapse", Components: 5},
			}
		}},
		{"backups", openBackups},
		{"restore", func(a *App) {
			a.jump(a.index("Restore"))
			day := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
			for i, size := range []int64{412 << 20, 409 << 20, 397 << 20, 371 << 20} {
				at := day.AddDate(0, 0, -i*7)
				a.cur().(*restoreScreen).list = append(a.cur().(*restoreScreen).list, deploy.LocalBackup{
					Path: "/home/demo/.local/share/interzone/backups/example.org/" + at.Format("2006-01-02-1504") + ".sql.gz", Time: at, Size: size})
			}
		}},
		{"alerts", openAlerts},
		{"maintenance", func(a *App) { deliver(a, maintainMsg("vacuum")) }},
		{"tuning", func(a *App) { // the roles of INTERZONE_PLAYBOOK, as shots.sh sets it
			a.cfg.PlaybookDir = os.Getenv("INTERZONE_PLAYBOOK")
			a.jump(a.index("Tuning"))
		}},
		{"servers", func(a *App) { a.jump(a.index("Servers")) }},
		{"move", func(a *App) { a.jump(a.index("Move")) }},
		{"mirrors", openMirrors},
		{"test", openTest},
		{"host-map", openSwarm},
	} {
		shots = append(shots, struct {
			name string
			open func(*App)
		}{"gallery-" + g.name, g.open})
	}
	for _, s := range shots {
		a := demoApp(t, 120, 36)
		if strings.HasPrefix(s.name, "gallery-") {
			a = demoApp(t, 142, 34)
		}
		a.cfg.Sources.Blackout = true // like the other shots
		if strings.HasPrefix(s.name, "wiki-") || strings.HasPrefix(s.name, "gallery-") {
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
		if strings.HasPrefix(s.name, "wiki-") || strings.HasPrefix(s.name, "gallery-") {
			a.status = "" // the demo's home can't be saved to
		}
		if err := os.WriteFile(filepath.Join(dir, s.name+".ans"), []byte(a.View()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	localMirror.Store(false)
}

// openSwarm is the README's map: the sample swarm with more nodes across Iran.
func openSwarm(a *App) {
	openHostMap(a)
	hs := a.cur().(*hostScreen)
	ip := netip.MustParseAddr
	for i, p := range []struct {
		addr          string
		ms            time.Duration
		sent, in, out float64
	}{
		{"2.180.0.1", 62, 7e9, 0, 4e6}, {"2.182.32.1", 55, 5e9, 0, 2e6}, {"2.182.214.1", 48, 4e9, 3e5, 1e6},
		{"2.182.169.1", 30, 3e9, 0, 0}, {"2.183.156.1", 70, 2e9, 2e6, 0}, {"2.183.80.1", 38, 2e9, 0, 5e5},
		{"2.184.160.1", 90, 1e9, 0, 0}, {"2.181.112.1", 110, 8e8, 1e6, 0}, {"2.183.131.1", 75, 6e8, 0, 0},
		{"2.187.32.1", 66, 4e8, 0, 3e5}, {"2.183.0.1", 58, 3e8, 0, 0},
	} {
		id := string(rune('e' + i))
		hs.stats.PeerList = append(hs.stats.PeerList, deploy.Peer{ID: id, Addr: ip(p.addr), Sent: int64(p.sent), Latency: p.ms * time.Millisecond, Inbound: i%3 == 0})
		if p.in+p.out > 0 {
			hs.rates[id] = peerRate{in: p.in, out: p.out}
			hs.peerHist[id] = []float64{p.out / 2, p.in + p.out, (p.in + p.out) * 0.8}
		}
	}
	hs.stats.Peers = len(hs.stats.PeerList)
	hs.ripples = append(hs.ripples, ripple{addr: ip("2.183.80.1"), start: 0.2, join: true})
}

func openConfirm(a *App) {
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
}

func openManage(a *App) {
	a.jump(a.index("Manage"))
	a.health = &deploy.Health{Up: true, Trusted: true, CertLeft: 89 * 24 * time.Hour}
}

func openBackups(a *App) {
	a.jump(a.index("Backups"))
	a.cur().(*backupsScreen).form.get("on").on = true
}

func openAlerts(a *App) {
	a.jump(a.index("Alerts"))
	f := a.cur().(*alertsScreen).form
	f.get("on").on = true
	f.get("format").choice = 2
	f.get("room").input.SetValue("#alerts:example.org")
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
