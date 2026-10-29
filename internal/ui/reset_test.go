package ui

import (
	"os"
	"path/filepath"
	"testing"

	"interzone/internal/config"
)

// Starting fresh moves interzone's folders aside, keeps them out of the
// new ones, and opens the wizard on empty answers.
func TestStartFresh(t *testing.T) {
	a := demoApp(t, 120, 36)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a.cfg.MarkDeployed()
	_ = config.SetActive("work")
	if err := a.cfg.Save(); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(config.DataDir(), "backups"), 0o700)

	a.jump(a.index("Servers"))
	s := a.cur().(*serversScreen)
	s.update(runes("R"))
	if !s.reset {
		t.Fatal("R didn't ask first")
	}
	msg := s.update(runes("R"))()
	a.Update(msg)

	if a.cfg.Domain != "" || a.cfg.Deployed != nil || a.cur().name() != "Server" {
		t.Errorf("not fresh: domain %q, on %s", a.cfg.Domain, a.cur().name())
	}
	if c, err := config.LoadFile(config.Path()); err != nil || c.Domain != "" || config.Active() != config.DefaultProfile {
		t.Errorf("the old answers were saved back: %v", err)
	}
	if _, err := os.Stat(config.ProfilePath("work")); err == nil {
		t.Error("the old server's answers were saved back")
	}
	for _, d := range []string{config.Dir(), config.DataDir()} {
		old, _ := filepath.Glob(d + ".old-*")
		if len(old) != 1 {
			t.Errorf("%s wasn't moved aside: %v", d, old)
		}
	}
	if old, _ := filepath.Glob(config.DataDir() + ".old-*/backups"); len(old) != 1 {
		t.Error("the backups weren't kept")
	}
}
