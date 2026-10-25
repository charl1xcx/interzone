// interzone is a friendly terminal wizard for matrix-docker-ansible-deploy
// that also works from behind a national internet blackout.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/cli"
	"interzone/internal/config"
	"interzone/internal/deploy"
	"interzone/internal/ui"
)

var version = "dev"

func main() {
	deploy.Version = version
	if len(os.Args) > 1 {
		if os.Args[1] == "help" {
			fmt.Println(cli.Usage)
			return
		}
		if cmd, ok := cli.Commands[os.Args[1]]; ok {
			if err := cmd(os.Args[2:]); err != nil {
				if !errors.Is(err, cli.ErrFailed) {
					fmt.Fprintln(os.Stderr, "interzone:", err)
				}
				os.Exit(1)
			}
			return
		}
	}

	showVersion := flag.Bool("version", false, "print the version and exit")
	reset := flag.Bool("reset", false, "forget saved answers and start fresh")
	layout := flag.String("layout", "", "auto, compact (phones, narrow or portrait terminals) or wide; the default is the last one picked in the app, else auto")
	noMouse := flag.Bool("no-mouse", false, "don't take the mouse, so the terminal can select text without shift")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), cli.Usage+"\n\nOptions:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "interzone: unknown command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("interzone", version)
		return
	}

	cfg, loaded := config.Load()
	if *reset {
		cfg, loaded = config.Default(), false
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if !*noMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	app := ui.New(cfg, loaded)
	if *layout != "" {
		if *layout != "auto" && *layout != "compact" && *layout != "wide" {
			fmt.Fprintf(os.Stderr, "interzone: -layout is auto, compact or wide, not %q\n", *layout)
			os.Exit(2)
		}
		app.SetLayout(*layout)
	}
	p := tea.NewProgram(app, opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "interzone:", err)
		os.Exit(1)
	}
}
