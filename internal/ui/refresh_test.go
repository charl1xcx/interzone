package ui

import (
	"testing"
	"time"

	"interzone/internal/deploy"
)

// A dashboard left while it refreshes still gets its answer, so it doesn't
// wait for it forever.
func TestResultReachesItsScreen(t *testing.T) {
	a := demoApp(t, 120, 36)
	openDashboard(a)
	ds := a.cur().(*dashboardScreen)
	ds.loading = true
	a.jump(a.index("Users"))
	a.Update(statusMsg{server: ds.server, st: &deploy.ServerStatus{Taken: time.Now(), Hostname: "fresh"}, certs: true})
	if ds.loading || ds.st.Hostname != "fresh" {
		t.Fatalf("the dashboard missed its answer: loading %v, host %q", ds.loading, ds.st.Hostname)
	}
}

func TestDashboardRefreshesItself(t *testing.T) {
	a := demoApp(t, 120, 36)
	openDashboard(a)
	ds := a.cur().(*dashboardScreen)
	certs := ds.st.Certs
	if a.Update(statusMsg{server: ds.server, st: &deploy.ServerStatus{Taken: time.Now()}}); ds.gen != 1 {
		t.Fatalf("no refresh scheduled after an answer (gen %d)", ds.gen)
	}
	if len(ds.st.Certs) != len(certs) {
		t.Errorf("an answer without certificates dropped the last ones")
	}
	if _, cmd := a.Update(dashTickMsg{server: ds.server, gen: 0}); cmd != nil || ds.loading {
		t.Errorf("an old tick refreshed")
	}
	if _, cmd := a.Update(dashTickMsg{server: ds.server, gen: 1}); cmd == nil || !ds.loading {
		t.Errorf("the tick didn't refresh")
	}
	ds.loading = false
	a.jump(a.index("Users"))
	if a.Update(statusMsg{server: ds.server, st: &deploy.ServerStatus{Taken: time.Now()}}); ds.gen != 1 {
		t.Errorf("refreshing while off screen")
	}
}

// A change shows on the list straight away, and the list stays usable
// while it's refreshed behind it.
func TestUsersChangeShowsAtOnce(t *testing.T) {
	a := demoApp(t, 120, 36)
	openUsers(a)
	us := a.cur().(*usersScreen)
	a.Update(userOpMsg{op: &userOp{user: "@bob:example.org", body: map[string]any{"deactivated": true}}, info: "@bob:example.org deactivated"})
	var bob deploy.MatrixUser
	for _, u := range us.list.Users {
		if u.ID == "@bob:example.org" {
			bob = u
		}
	}
	if !bob.Deactivated || bob.Rooms != 0 || bob.Devices != 0 {
		t.Errorf("bob isn't shown deactivated: %+v", bob)
	}
	if us.busy || !us.syncing {
		t.Errorf("busy %v, syncing %v; want the list usable and refreshing", us.busy, us.syncing)
	}
	n := len(us.list.Users)
	a.Update(usersMsg{server: us.server, gen: us.gen - 1, list: &deploy.UserList{}})
	if len(us.list.Users) != n {
		t.Errorf("an older reload replaced the list")
	}
	a.Update(usersMsg{server: us.server, gen: us.gen, list: &deploy.UserList{Rooms: 1}})
	if us.syncing || us.list.Rooms != 1 {
		t.Errorf("the latest reload wasn't taken")
	}
}
