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
		// The README's map: the sample swarm with more nodes across Iran.
		{"host-map", func(a *App) {
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
		}},
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
	localMirror.Store(false)
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
