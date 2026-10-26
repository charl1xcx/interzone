package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
	}
	for _, s := range shots {
		a := demoApp(t, 120, 36)
		a.cfg.Sources.Blackout = true // like the other shots
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(true)
		s.open(a)
		if err := os.WriteFile(filepath.Join(dir, s.name+".ans"), []byte(a.View()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
}
