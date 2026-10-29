package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"interzone/internal/deploy"
)

// Add users › from a CSV: a bad file is shown on the form; a good one
// saves the passwords and starts the run.
func TestBulkUsersForm(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Manage"))
	s := a.cur().(*manageScreen)
	s.choose("user")
	s.userForm.get("how").choice = 1
	if s.userForm.get("name").visible() || !s.userForm.get("csv").visible() {
		t.Fatal("the CSV choice didn't swap the fields")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "team.csv")
	_ = os.WriteFile(path, []byte("al ice\n"), 0o644)
	s.userForm.get("csv").input.SetValue(path)
	if cmd := s.register(); cmd != nil || !strings.Contains(s.view(100, 30), "valid username") {
		t.Fatalf("bad CSV: %q", s.userErr)
	}
	_ = os.WriteFile(path, []byte("alice\nbob,,admin\n"), 0o644)
	cmd := s.register()
	if cmd == nil {
		t.Fatal(s.userErr)
	}
	m, ok := cmd().(runActionMsg)
	if !ok || m.opts.Action != deploy.ActionBulkUsers || len(m.opts.Users) != 2 || !m.opts.Users[1].Admin ||
		m.opts.Output != filepath.Join(dir, "team-passwords.csv") {
		t.Fatalf("%+v", m)
	}
	if _, err := os.Stat(m.opts.Output); err != nil {
		t.Error(err)
	}
}
