package deploy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"interzone/internal/config"
)

// A dry run lists the tasks reporting changes, and a stop in check mode
// is a note, not a failure.
func TestDryRun(t *testing.T) {
	bin := t.TempDir()
	stub := "#!/bin/sh\ncat <<'OUT'\nTASK [matrix-synapse : Ensure Synapse config]\nok: [x]\nTASK [matrix-synapse : Ensure Synapse image]\nchanged: [x]\nTASK [traefik : Ensure Traefik is started]\nchanged: [x] => (item=a)\nchanged: [x] => (item=b)\nTASK [postgres : Run a command]\nfatal: [x]: FAILED!\nOUT\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	c := config.Default()
	c.PlaybookDir = t.TempDir()
	r := &Runner{cfg: c, events: make(chan Event, 256)}
	r.env = Env(c)
	go func() {
		for range r.events {
		}
	}()
	if err := r.dryRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(r.events)
	want := []string{"matrix-synapse : Ensure Synapse image", "traefik : Ensure Traefik is started"}
	if !slices.Equal(r.result.WouldChange, want) || r.result.StoppedAt != "postgres : Run a command" {
		t.Errorf("got %q, stopped at %q", r.result.WouldChange, r.result.StoppedAt)
	}
}
