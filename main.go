// interzone is a friendly terminal wizard for matrix-docker-ansible-deploy
// that also works from behind a national internet blackout.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

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
		if cmd, ok := cli.Commands[os.Args[1]]; ok {
			if err := cmd(os.Args[2:]); err != nil {
				var pe *fs.PathError
				if errors.As(err, &pe) && errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintln(os.Stderr, "interzone: there's nothing at", pe.Path)
				} else if !errors.Is(err, cli.ErrFailed) {
					fmt.Fprintln(os.Stderr, "interzone:", err)
				}
				os.Exit(1)
			}
			return
		}
	}

	showVersion := flag.Bool("version", false, "print the version and exit")
	reset := flag.Bool("reset", false, "start fresh: move interzone's config and data folders aside")
	theme := flag.String("theme", "", "the colours for this run: "+strings.Join(ui.ThemeNames(), ", ")+"; ctrl+y in the app switches and remembers them")
	layout := flag.String("layout", "", "auto, compact (phones, narrow or portrait terminals) or wide; the default is the last one picked in the app, else auto")
	noMouse := flag.Bool("no-mouse", false, "don't take the mouse, so the terminal can select text without shift")
	plain := flag.Bool("plain", os.Getenv("INTERZONE_PLAIN") != "", "no colours, and ASCII instead of boxes, bars and an animated spinner: for screen readers and logs (also INTERZONE_PLAIN=1)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), cli.Usage+"\n\nOptions:")
		flag.PrintDefaults()
	}
	if len(os.Args) > 1 && os.Args[1] == "help" {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.Usage()
		return
	}
	flag.Parse()
	if flag.NArg() > 0 {
		if _, ok := cli.Commands[flag.Arg(0)]; ok {
			fmt.Fprintf(os.Stderr, "interzone: %q is a command; put it first, with its own options after it\n", flag.Arg(0))
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "interzone: unknown command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
	// Check the options before -reset moves anything aside.
	if *layout != "" && *layout != "auto" && *layout != "compact" && *layout != "wide" {
		fmt.Fprintf(os.Stderr, "interzone: -layout is auto, compact or wide, not %q\n", *layout)
		os.Exit(2)
	}
	if *theme != "" && !slices.Contains(ui.ThemeNames(), *theme) {
		fmt.Fprintf(os.Stderr, "interzone: -theme is one of %s, not %q\n", strings.Join(ui.ThemeNames(), ", "), *theme)
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("interzone", version)
		return
	}

	if *reset {
		moved, err := config.Reset()
		for _, d := range moved {
			fmt.Fprintln(os.Stderr, "interzone: moved aside to", d)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "interzone: couldn't reset:", err)
			os.Exit(1)
		}
	}
	cfg, loaded := config.Load()

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if !*noMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	if *plain {
		ui.SetPlain() // before New: the screens' spinners are made there
	}
	app := ui.New(cfg, loaded)
	if *layout != "" {
		app.SetLayout(*layout)
	}
	if *theme != "" {
		ui.SetTheme(*theme)
	}
	p := tea.NewProgram(app, opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "interzone:", err)
		os.Exit(1)
	}
}
