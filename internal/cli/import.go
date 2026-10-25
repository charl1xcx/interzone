package cli

import (
	"flag"
	"fmt"
	"strings"

	"interzone/internal/catalog"
	"interzone/internal/config"
)

func init() { Commands["import"] = Import }

// Import takes over a server set up with matrix-docker-ansible-deploy
// without interzone.
func Import(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	name := fs.String("name", "", "name for the server in interzone (default: from the domain)")
	dry := fs.Bool("dry-run", false, "show what would be imported and save nothing")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage: interzone import [options] PATH

Takes over a server set up with matrix-docker-ansible-deploy by hand. PATH is
the playbook checkout, its inventory directory, or the server's vars.yml.
Nothing on the server changes, and the original files stay as they are: a
checkout is copied for interzone to work in.

Options:`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return ErrFailed
	}
	base, _ := config.Load()
	im, err := config.ImportMDAD(fs.Arg(0), base)
	if err != nil {
		return err
	}
	printImport(im)
	if *dry {
		fmt.Println()
		fmt.Println(muted.Render("Dry run: nothing saved."))
		return nil
	}
	if *name == "" {
		*name = config.SuggestName(im.Config.Domain)
	}
	if err := im.Save(*name); err != nil {
		return err
	}
	if err := config.SetActive(*name); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(good.Render("✓ Saved as server " + *name + " and made it the active one."))
	if im.PlaybookDir != "" {
		fmt.Println(muted.Render("  The playbook was copied to " + im.Config.PlaybookDir + "; " + im.PlaybookDir + " is untouched."))
	}
	fmt.Println(muted.Render("  Run interzone to manage it. The next update writes vars.yml from these answers;"))
	fmt.Println(muted.Render("  Review lists what changes in it first."))
	return nil
}

func printImport(im *config.Import) {
	c := im.Config
	hs := c.Homeserver
	for _, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			hs = h.Name
		}
	}
	kv := func(k, v string) { fmt.Printf("  %-12s %s\n", muted.Render(k), v) }
	fmt.Println(head.Render("==> " + im.VarsFile))
	kv("Domain", c.Domain)
	kv("Homeserver", hs)
	where := im.Where()
	if im.HostsFile == "" {
		where += muted.Render(" (no inventory/hosts found)")
	}
	kv("Server", where)
	kv("Components", strings.Join(im.ComponentNames(), ", "))
	if im.PlaybookDir != "" {
		kv("Playbook", im.PlaybookDir+muted.Render(" (pulls from "+c.Sources.PlaybookGit+")"))
	}
	list := func(style func(...string) string, title string, keys []string) {
		if len(keys) == 0 {
			return
		}
		fmt.Println()
		fmt.Println(style(fmt.Sprintf(title, len(keys))))
		for _, k := range keys {
			fmt.Println("    " + k)
		}
	}
	list(good.Render, "✓ %d variable(s) interzone has no setting for, kept as you wrote them (extra vars):", im.Kept)
	list(good.Render, "✓ %d variable(s) interzone also sets, kept at your values:", im.Override)
	list(good.Render, "✓ %d variable(s) interzone would add, held at the playbook's defaults:", im.Pinned)
	list(warn.Render, "! %d variable(s) interzone adds whose defaults couldn't be checked; look them over on Review:", im.Added)
	if len(im.Warnings) > 0 {
		fmt.Println()
		for _, w := range im.Warnings {
			fmt.Println(warn.Render("! " + w))
		}
	}
}
