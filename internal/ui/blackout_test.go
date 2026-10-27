package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/config"
)

// Choosing blackout points every download back at the in-country mirrors.
func TestBlackoutSources(t *testing.T) {
	a := demoApp(t, 120, 36)
	a.jump(a.index("Server"))
	s := a.cur().(*serverScreen)
	f := s.form
	f.get("net").choice = 0
	f.get("useRegistry").on = false
	f.get("registry").input.SetValue("elsewhere.example/")
	f.get("git").input.SetValue("https://github.com/spantaleev/matrix-docker-ansible-deploy")
	for i, fl := range f.fields {
		if fl.key == "net" {
			f.focusOn(i)
		}
	}
	press(a, tea.KeyMsg{Type: tea.KeyRight})
	if f.value("net") != "blackout" {
		t.Fatalf("net = %q", f.value("net"))
	}
	for k, want := range map[string]string{
		"useRegistry": "true", "registry": config.DefaultRegistryPrefix, "git": config.DefaultPlaybookGit,
		"roles": config.DefaultRolesGitBase, "apt": config.SelfHostedMirror,
		"dockerpkg": config.SelfHostedMirror + "/docker", "pypi": config.SelfHostedMirror + "/pypi/simple",
	} {
		if got := f.value(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
