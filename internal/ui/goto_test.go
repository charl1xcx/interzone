package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func gotoType(a *App, s string) {
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// ctrl+k goes to a screen, or to a Manage action; one that changes the
// server waits for a second enter.
func TestGoto(t *testing.T) {
	a := demoApp(t, 120, 36)
	if a.cfg.Installed() {
		t.Fatal("the demo shouldn't be installed")
	}
	gotoType(a, "manage backups")
	if a.gotoOpen || a.cur().name() == "Backups" || a.status == "" {
		t.Errorf("an action on a server not installed should say why, at %s", a.cur().name())
	}
	a.cfg.PlaybookDir = t.TempDir()
	vars := filepath.Join(a.cfg.PlaybookDir, "inventory", "host_vars", a.cfg.HostName(), "vars.yml")
	os.MkdirAll(filepath.Dir(vars), 0o755)
	os.WriteFile(vars, nil, 0o600)
	gotoType(a, "fine tune")
	if a.gotoOpen || a.cur().name() != "Tuning" {
		t.Fatalf("ctrl+k fine tune went to %s", a.cur().name())
	}
	gotoType(a, "back up the")
	ms, ok := a.cur().(*manageScreen)
	if !ok || !ms.armed || ms.actions()[ms.cursor].id != "backup" {
		t.Fatalf("ctrl+k back up should arm the backup on Manage, at %s", a.cur().name())
	}
	gotoType(a, "zzz")
	if !a.gotoOpen {
		t.Error("enter with nothing matching closed the box")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.gotoOpen || a.cur().name() != "Manage" {
		t.Error("esc should close the box and stay put")
	}
}
