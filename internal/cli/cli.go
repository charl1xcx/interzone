// Package cli implements interzone's non-interactive commands: apply,
// check, bundle, unbundle and the maintenance commands in tools.go. They print plain progress lines (coloured
// only on a terminal) and report failure through the exit code.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

var (
	good  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3EF0B0"))
	warn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFC56B"))
	bad   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B81"))
	head  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9D7CFF")).Bold(true)
	muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A86A3"))
)

// Commands maps subcommand names to their entry points.
var Commands = map[string]func(args []string) error{
	"apply":    Apply,
	"check":    Check,
	"bundle":   Bundle,
	"unbundle": Unbundle,
	"mirrors":  Mirrors,
}

const Usage = `Usage:
  interzone                 start the wizard
  interzone apply           deploy with the saved answers, no questions asked
  interzone check           test mirrors, the server and DNS
  interzone mirrors         test every known in-country mirror
  interzone bundle          pack the playbook, roles and Ansible for offline use
  interzone unbundle FILE   install an offline bundle on this machine
  interzone changes         show what an update would bring
  interzone images          check every container image can be pulled
  interzone status          the server's load, disks, certificates, containers
  interzone restore [FILE]  list backups, or restore the database from one
  interzone rollback [N]    list earlier deploys, or go back to one
  interzone servers         list servers; 'servers use NAME' switches
  interzone import PATH     take over a server set up with the playbook by hand
  interzone self-update     install the newest interzone release

INTERZONE_SERVER=NAME runs any command against another saved server.

Run 'interzone COMMAND -h' for a command's options.`

// ErrFailed is returned when a command ran but its checks or run failed;
// the details have already been printed.
var ErrFailed = errors.New("failed")

// configFlag parses a command whose only flag is --config, and loads it.
func configFlag(name string, args []string) (*config.Config, error) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	_ = fs.Parse(args)
	c, _, err := loadConfig(*file)
	return c, err
}

func loadConfig(file string) (*config.Config, string, error) {
	if file == "" {
		c, ok := config.Load()
		if !ok {
			return nil, "", fmt.Errorf("no saved answers in %s; run interzone once or pass --config", config.Path())
		}
		return c, config.Path(), nil
	}
	c, err := config.LoadFile(file)
	return c, file, err
}

func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Apply runs a full deploy (or writes the files only) from saved answers.
func Apply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	saveOnly := fs.Bool("save-only", false, "write the inventory but don't run Ansible")
	skipChecks := fs.Bool("skip-checks", false, "don't check the server and DNS first")
	force := fs.Bool("force", false, "deploy even if checks fail")
	quiet := fs.Bool("quiet", false, "print task names and errors only, not every Ansible line")
	latest := fs.Bool("latest", false, "release a playbook held back by a rollback and update it")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone apply [options]\n\nDeploys with saved answers. Secrets that are still missing are generated and saved back.\n\nOptions:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	c, path, err := loadConfig(*file)
	if err != nil {
		return err
	}
	c.FillGenerated()
	if *latest {
		c.PinnedCommit = ""
	} else if c.PinnedCommit != "" {
		fmt.Println(warn.Render("! The playbook is held at " + c.PinnedCommit[:min(10, len(c.PinnedCommit))] + " since a rollback; pass --latest to update it."))
	}
	if err := c.SaveFile(path); err != nil {
		return fmt.Errorf("saving generated secrets: %w", err)
	}
	if missing := c.Missing(); len(missing) > 0 {
		fmt.Println(bad.Render("✗ Required settings are missing:"))
		for _, m := range missing {
			fmt.Println("  • " + m)
		}
		return ErrFailed
	}

	if !*skipChecks && !*saveOnly {
		fmt.Println(head.Render("==> Pre-deploy checks"))
		fs := deploy.ServerChecks(c)
		printFindings(fs)
		if deploy.Worst(fs) == deploy.Fail {
			if !*force {
				fmt.Println(bad.Render("✗ Checks failed. Fix them, or pass --force to deploy anyway."))
				return ErrFailed
			}
			fmt.Println(warn.Render("! Checks failed; continuing because of --force."))
		}
		fmt.Println()
	}

	action := deploy.ActionInstall
	if *saveOnly {
		action = deploy.ActionSaveOnly
	}
	res, err := runAction(c, deploy.Options{Action: action}, *quiet)
	if err != nil {
		return err
	}
	if *saveOnly {
		fmt.Println(good.Render("✓ Files written. Deploy later with: ") +
			"cd " + c.PlaybookDir + " && ansible-playbook -i inventory/hosts setup.yml --tags=install-all,ensure-matrix-users-created,start")
		return nil
	}
	fmt.Println(good.Render("✓ Deployed: ") + "https://matrix." + c.Domain)
	printSelfCheck(res)
	if len(res.Problems) > 0 {
		return ErrFailed
	}
	return nil
}

// Check tests the mirrors, the server and DNS.
func Check(args []string) error {
	c, err := configFlag("check", args)
	if err != nil {
		return err
	}

	fmt.Println(head.Render("==> Sources"))
	worst := deploy.Pass
	for _, ch := range deploy.Preflight(c) {
		f := deploy.Finding{Group: "Sources", Name: ch.Name, Detail: ch.Target + "  " + ch.Detail}
		if !ch.OK {
			f.Level = deploy.Fail
			if strings.Contains(ch.Detail, "will be installed") {
				f.Level = deploy.Warn
			}
		}
		worst = max(worst, f.Level)
		printFindings([]deploy.Finding{f})
	}
	fmt.Println()
	fmt.Println(head.Render("==> Server and DNS"))
	fs2 := deploy.ServerChecks(c)
	printFindings(fs2)
	fmt.Println()
	fmt.Println(head.Render("==> Ports to open in a firewall in front of the server"))
	for _, p := range c.Ports() {
		fmt.Printf("  %-40s %s\n", p.For, muted.Render(p.String()))
	}
	if max(worst, deploy.Worst(fs2)) == deploy.Fail {
		return ErrFailed
	}
	return nil
}

// Mirrors tests every known in-country mirror and shows which work.
func Mirrors(args []string) error {
	fs := flag.NewFlagSet("mirrors", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone mirrors\n\nTests every known mirror in Iran for OS packages, Docker Engine and PyPI, and\nshows which distros each one carries. OS mirrors count for a distro only\nif its security or update indexes are fresh; PyPI mirrors must list\nansible-core.")
	}
	_ = fs.Parse(args)
	fmt.Println(muted.Render("Testing " + fmt.Sprint(len(config.KnownMirrors)) + " mirrors…"))
	titles := map[config.MirrorKind]string{
		config.MirrorOS: "OS packages", config.MirrorDocker: "Docker Engine packages", config.MirrorPyPI: "PyPI",
	}
	working := 0
	var kind config.MirrorKind
	for _, r := range deploy.ProbeKnownMirrors() {
		if r.Mirror.Kind != kind {
			kind = r.Mirror.Kind
			fmt.Println()
			fmt.Println(head.Render("==> " + titles[kind]))
		}
		icon, style := good.Render("✓"), muted
		if r.OK {
			working++
		} else {
			icon, style = bad.Render("✗"), bad
		}
		fmt.Printf("  %s  %-58s %s\n", icon, r.Mirror.URL, style.Render(fmt.Sprintf("%s  (%.1fs)", r.Detail, r.Took.Seconds())))
	}
	fmt.Println()
	fmt.Printf("%d of %d mirrors work.\n", working, len(config.KnownMirrors))
	if working == 0 {
		return ErrFailed
	}
	return nil
}

// Bundle packs everything a blackout deploy needs except container images.
func Bundle(args []string) error {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	file := fs.String("config", "", "answers file to take mirrors from (default: the wizard's saved answers, or the defaults)")
	out := fs.String("o", "", "output file (default: interzone-bundle-DATE.tar.gz)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage: interzone bundle [options]

Packs the playbook with its Ansible roles, Python packages for Ansible and
this binary into one .tar.gz for a machine without internet. Container
images are not included. Python packages match this machine's OS, CPU and
Python version.

Options:`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	c := config.Default()
	if *file != "" || fileExists(config.Path()) {
		var err error
		if c, _, err = loadConfig(*file); err != nil {
			return err
		}
	}
	res, err := runAction(c, deploy.Options{Action: deploy.ActionBundle, Output: *out}, true)
	if err != nil {
		return err
	}
	fmt.Println(good.Render("✓ Bundle written: ") + res.File)
	fmt.Println(muted.Render("  On the offline machine: tar xzf it, then run ./interzone-bundle/interzone unbundle interzone-bundle"))
	return nil
}

// Unbundle installs a bundle made by Bundle.
func Unbundle(args []string) error {
	fs := flag.NewFlagSet("unbundle", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone unbundle FILE|DIR\n\nInstalls an offline bundle (the .tar.gz or its extracted directory). An existing inventory is kept.")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return ErrFailed
	}
	c, _ := config.Load()
	info, err := deploy.Unbundle(fs.Arg(0), c.PlaybookDir, func(s string) { fmt.Println(muted.Render("  " + s)) })
	if err != nil {
		return err
	}
	fmt.Println(good.Render("✓ Bundle installed") + muted.Render(fmt.Sprintf(" (made %s by interzone %s, playbook %.10s)",
		info.Created.Local().Format("2006-01-02"), info.Interzone, info.Playbook)))
	fmt.Println(muted.Render("  Run interzone and point the container registry at a mirror inside your network."))
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// runAction runs a deploy action, printing steps and output as it goes.
func runAction(c *config.Config, opts deploy.Options, quiet bool) (*deploy.Result, error) {
	ctx, cancel := interruptible()
	defer cancel()
	_ = os.MkdirAll(config.Dir(), 0o700)
	logFile, _ := os.OpenFile(config.Dir()+"/last-deploy.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if logFile != nil {
		defer logFile.Close()
	}
	var out io.Writer = os.Stdout
	start := time.Now()
	for ev := range deploy.Start(ctx, c, opts) {
		if ev.Line != "" && logFile != nil {
			fmt.Fprintln(logFile, ev.Line)
		}
		switch {
		case ev.Plan != nil:
		case ev.Err != nil:
			fmt.Fprintln(out, bad.Render("✗ "+ev.Err.Error()))
			fmt.Fprintln(out, muted.Render("  full log: "+config.Dir()+"/last-deploy.log"))
			return nil, ErrFailed
		case ev.Done:
			fmt.Fprintln(out, muted.Render(fmt.Sprintf("finished in %s", time.Since(start).Round(time.Second))))
			return ev.Result, nil
		case ev.Started:
			fmt.Fprintln(out, head.Render("==> "+ev.Step.String()))
		case ev.Skipped:
			fmt.Fprintln(out, muted.Render("--- "+ev.Step.String()+": "+ev.Line))
		case ev.Warn:
			fmt.Fprintln(out, warn.Render("!   "+ev.Step.String()+" finished with problems"))
		case ev.Failed:
			fmt.Fprintln(out, bad.Render("    "+ev.Line))
		case quiet && ev.Task == "" && !ev.Note && !strings.HasPrefix(ev.Line, "$ "):
		case ev.Task != "":
			fmt.Fprintln(out, "    "+ev.Line)
		default:
			fmt.Fprintln(out, muted.Render("    "+ev.Line))
		}
	}
	return nil, fmt.Errorf("the run ended unexpectedly")
}

func printFindings(fs []deploy.Finding) {
	svcW := 0
	for _, f := range fs {
		svcW = max(svcW, min(32, len(f.Service)))
	}
	for _, f := range fs {
		icon, style := good.Render("✓"), muted
		switch f.Level {
		case deploy.Warn:
			icon, style = warn.Render("!"), warn
		case deploy.Fail:
			icon, style = bad.Render("✗"), bad
		}
		if svcW > 0 {
			fmt.Printf("  %s  %s %-24s %s\n", icon, muted.Render(fmt.Sprintf("%-*s", svcW, f.Service)), f.Name, style.Render(f.Detail))
			continue
		}
		fmt.Printf("  %s  %-24s %s\n", icon, f.Name, style.Render(f.Detail))
	}
}

func printSelfCheck(res *deploy.Result) {
	if res == nil {
		return
	}
	if len(res.Passed) > 0 {
		fmt.Println(good.Render("✓ Working: ") + strings.Join(res.Passed, ", "))
	}
	for _, p := range res.Problems {
		fmt.Println(bad.Render("✗ ") + p)
	}
}
