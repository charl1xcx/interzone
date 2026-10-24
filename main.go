// interzone is a friendly terminal wizard for matrix-docker-ansible-deploy
// that also works from behind a national internet blackout.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/config"
	"interzone/internal/ui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	reset := flag.Bool("reset", false, "forget saved answers and start fresh")
	flag.Parse()

	if *showVersion {
		fmt.Println("interzone", version)
		return
	}

	cfg, loaded := config.Load()
	if *reset {
		cfg, loaded = config.Default(), false
	}

	p := tea.NewProgram(ui.New(cfg, loaded), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "interzone:", err)
		os.Exit(1)
	}
}
