package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

func init() {
	Commands["servers"] = Servers
	Commands["self-update"] = SelfUpdate
	Commands["changes"] = Changes
	Commands["images"] = Images
	Commands["status"] = Status
	Commands["rollback"] = Rollback
	Commands["restore"] = Restore
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := interruptible()
	ctx, cancel2 := context.WithTimeout(ctx, d)
	return ctx, func() { cancel2(); cancel() }
}

// Servers lists the saved servers, or switches or forgets one.
func Servers(args []string) error {
	fs := flag.NewFlagSet("servers", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone servers [use NAME | rm NAME]\n\nLists the servers interzone has answers for. 'use' makes one the active server\nfor the wizard and every command; INTERZONE_SERVER=NAME picks one for a single run.\n'rm' forgets a server's answers; nothing on the server changes.")
	}
	_ = fs.Parse(args)
	switch fs.Arg(0) {
	case "":
		list := config.Profiles()
		if len(list) == 0 {
			fmt.Println(muted.Render("No saved servers yet; run interzone to set one up."))
			return nil
		}
		for _, p := range list {
			mark := "  "
			if p.Name == config.Active() {
				mark = good.Render("● ")
			}
			fmt.Printf("%s%-16s %-28s %s\n", mark, p.Name, p.Domain, muted.Render(p.Host))
		}
		return nil
	case "use":
		name := fs.Arg(1)
		if _, err := os.Stat(config.ProfilePath(name)); name == "" || err != nil {
			return fmt.Errorf("no server called %q; 'interzone servers' lists them", name)
		}
		if err := config.SetActive(name); err != nil {
			return err
		}
		fmt.Println(good.Render("✓ Active server: ") + name)
		return nil
	case "rm":
		name := fs.Arg(1)
		if name == config.Active() {
			return fmt.Errorf("%s is the active server; switch to another first", name)
		}
		if err := config.RemoveProfile(name); err != nil {
			return err
		}
		fmt.Println(good.Render("✓ Forgot ") + name)
		return nil
	}
	fs.Usage()
	return ErrFailed
}

// SelfUpdate replaces this binary with the newest release.
func SelfUpdate(args []string) error {
	fs := flag.NewFlagSet("self-update", flag.ExitOnError)
	force := fs.Bool("force", false, "reinstall even if this is the newest release")
	_ = fs.Parse(args)
	c, ok := config.Load()
	if !ok {
		c = config.Default()
	}
	res, err := runAction(c, deploy.Options{Action: deploy.ActionSelfUpdate, Force: *force}, true)
	if err != nil {
		return err
	}
	if res.File == "" {
		fmt.Println(good.Render("✓ interzone " + res.Version + " is the newest release"))
	} else {
		fmt.Println(good.Render("✓ Installed interzone "+res.Version+" to ") + res.File)
	}
	return nil
}

// Changes previews what 'apply' would update.
func Changes(args []string) error {
	fs := flag.NewFlagSet("changes", flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	_ = fs.Parse(args)
	c, _, err := loadConfig(*file)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(3 * time.Minute)
	defer cancel()
	ch, err := deploy.PreviewUpdate(ctx, c)
	if err != nil {
		return err
	}
	if c.PinnedCommit != "" {
		fmt.Println(warn.Render("! The playbook is held at " + c.PinnedCommit[:min(10, len(c.PinnedCommit))] + " since a rollback; 'apply --latest' releases it."))
	}
	if ch.From == ch.To {
		fmt.Println(good.Render("✓ The playbook is up to date ") + muted.Render("("+ch.From[:10]+")"))
		return nil
	}
	fmt.Println(head.Render(fmt.Sprintf("==> %d new commit(s), %s → %s", ch.Total, ch.From[:10], ch.To[:10])))
	for _, v := range ch.Versions {
		switch {
		case v.Old == "":
			fmt.Printf("  %-44s %s\n", v.Name, good.Render("new: "+v.New))
		case v.New == "":
			fmt.Printf("  %-44s %s\n", v.Name, muted.Render("removed (was "+v.Old+")"))
		default:
			fmt.Printf("  %-44s %s → %s\n", v.Name, muted.Render(v.Old), good.Render(v.New))
		}
	}
	if len(ch.Versions) > 0 {
		fmt.Println()
	}
	for _, cm := range ch.Commits {
		fmt.Printf("  %s  %s  %s\n", cm.Hash, muted.Render(cm.Date), cm.Subject)
	}
	if ch.Total > len(ch.Commits) {
		fmt.Println(muted.Render(fmt.Sprintf("  …and %d older", ch.Total-len(ch.Commits))))
	}
	return nil
}

// Images checks that every image the install needs can be pulled.
func Images(args []string) error {
	fs := flag.NewFlagSet("images", flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	_ = fs.Parse(args)
	c, _, err := loadConfig(*file)
	if err != nil {
		return err
	}
	res, err := runAction(c, deploy.Options{Action: deploy.ActionImages}, true)
	if err != nil {
		return err
	}
	if len(res.MissingImages) > 0 {
		fmt.Println(bad.Render(fmt.Sprintf("✗ %d of %d images can't be pulled:", len(res.MissingImages), len(res.MissingImages)+len(res.Images))))
		for _, m := range res.MissingImages {
			fmt.Println("  • " + m)
		}
		return ErrFailed
	}
	fmt.Println(good.Render(fmt.Sprintf("✓ All %d images can be pulled", len(res.Images))))
	return nil
}

// Status prints the server's load, disks, certificates and containers.
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	_ = fs.Parse(args)
	c, _, err := loadConfig(*file)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(2 * time.Minute)
	defer cancel()
	st, err := deploy.Status(ctx, c)
	if err != nil {
		return err
	}
	pct := func(a, b int64) string { return strconv.Itoa(int(float64(a)/float64(max(b, 1))*100+0.5)) + "%" }
	fmt.Println(head.Render("==> " + c.Domain))
	if st.CPUs > 0 {
		fmt.Printf("  %-14s %.2f %.2f %.2f on %d cores, up %s\n", "Load", st.Load[0], st.Load[1], st.Load[2], st.CPUs, st.Uptime.Round(time.Minute))
	}
	if st.MemTotal > 0 {
		used := st.MemTotal - st.MemAvail
		fmt.Printf("  %-14s %s of %s (%s)\n", "Memory", deploy.HumanSize(used), deploy.HumanSize(st.MemTotal), pct(used, st.MemTotal))
	}
	for _, d := range st.Disks {
		fmt.Printf("  %-14s %s free of %s (%s used)\n", "Disk "+d.Mount, deploy.HumanSize(d.Avail), deploy.HumanSize(d.Total), pct(d.Used, d.Total))
	}
	if st.Timer != "" && st.Timer != "n/a" && st.Timer != "0" {
		fmt.Printf("  %-14s %s\n", "Next backup", st.Timer)
	}
	failed := false
	if len(st.Certs) > 0 {
		fmt.Println(head.Render("==> Certificates"))
		svcW := 0
		for _, ct := range st.Certs {
			svcW = max(svcW, min(40, len(ct.Services)))
		}
		for _, ct := range st.Certs {
			style := good
			switch {
			case ct.Err != "" || ct.DaysLeft() < 7:
				style, failed = bad, true
			case ct.DaysLeft() < 21:
				style = warn
			}
			fmt.Printf("  %-*s %-32s %s\n", svcW, ct.Services, ct.Host, style.Render(ct.String()))
		}
	}
	fmt.Println(head.Render("==> Containers"))
	for _, ct := range st.Containers {
		icon := good.Render("●")
		if ct.State != "running" {
			icon, failed = bad.Render("●"), true
		}
		fmt.Printf("  %s %-40s %-8s %-22s %s\n", icon, ct.Name, ct.CPU, ct.Mem, muted.Render(ct.Status))
	}
	if failed {
		return ErrFailed
	}
	return nil
}

// Rollback lists earlier deploys or reinstalls one of them.
func Rollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	list := fs.Bool("list", false, "list the recorded deploys")
	quiet := fs.Bool("quiet", false, "print task names and errors only")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone rollback [--list | N]\n\nReinstalls deploy N from the list (1 is the latest) with the playbook commit\nand answers it used. The database is backed up first; today's answers are\nkept in config.before-rollback.json.\n\nOptions:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	hist := deploy.History()
	if *list || fs.NArg() == 0 {
		if len(hist) == 0 {
			fmt.Println(muted.Render("No deploys recorded yet."))
			return nil
		}
		for i, d := range hist {
			fmt.Printf("  %2d  %s  %.10s  %s %s\n", i+1, d.Time.Local().Format("2006-01-02 15:04"), d.Commit, d.Subject,
				muted.Render(fmt.Sprintf("(%s, %d components)", d.Homeserver, d.Components)))
		}
		return nil
	}
	if len(hist) == 0 {
		return fmt.Errorf("no deploys recorded yet; every successful install from now on is")
	}
	n, err := strconv.Atoi(fs.Arg(0))
	if err != nil || n < 1 || n > len(hist) {
		return fmt.Errorf("pick a deploy between 1 and %d; 'interzone rollback --list' shows them", len(hist))
	}
	cur, ok := config.Load()
	if !ok {
		return fmt.Errorf("no saved answers in %s", config.Path())
	}
	rc, err := deploy.RollbackConfig(cur, hist[n-1])
	if err != nil {
		return err
	}
	if err := cur.SaveFile(filepath.Join(config.Dir(), "config.before-rollback.json")); err != nil {
		return err
	}
	if err := rc.Save(); err != nil {
		return err
	}
	res, err := runAction(rc, deploy.Options{Action: deploy.ActionRollback}, *quiet)
	if err != nil {
		return err
	}
	fmt.Println(good.Render("✓ Rolled back to "+hist[n-1].Time.Local().Format("2006-01-02 15:04")) +
		muted.Render("; the playbook stays at "+hist[n-1].Commit[:min(10, len(hist[n-1].Commit))]+" until 'apply --latest'"))
	printSelfCheck(res)
	return nil
}

// Restore replaces the server's database with a dump.
func Restore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	file := fs.String("config", "", "answers file (default: the wizard's saved answers)")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	quiet := fs.Bool("quiet", false, "print task names and errors only")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: interzone restore [options] [FILE]\n\nWithout FILE, lists the backups on this computer. With FILE, stops every service,\ndumps the current database first, then replaces it with FILE.\n\nOptions:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	c, _, err := loadConfig(*file)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		list := deploy.ListBackups(c.Domain)
		if len(list) == 0 {
			fmt.Println(muted.Render("No backups of " + c.Domain + " in " + deploy.BackupDir()))
		}
		for _, b := range list {
			fmt.Printf("  %s  %8s  %s\n", b.Time.Local().Format("2006-01-02 15:04"), deploy.HumanSize(b.Size), b.Path)
		}
		return nil
	}
	path, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if !*yes {
		fmt.Printf("Replace the database of %s with %s? Messages since the dump are lost. [y/N] ", c.Domain, filepath.Base(path))
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" && answer != "yes" {
			return ErrFailed
		}
	}
	if _, err := runAction(c, deploy.Options{Action: deploy.ActionRestore, Backup: path}, *quiet); err != nil {
		return err
	}
	fmt.Println(good.Render("✓ Restored ") + filepath.Base(path))
	return nil
}
