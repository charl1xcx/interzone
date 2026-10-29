package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// Screens reached from Manage. Each does one maintenance job; the ones
// that run Ansible hand off to the Deploy screen with run().

func newSpinner() spinner.Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	if plain { // a redraw a second would have screen readers read it again
		sp.Spinner = spinner.Spinner{Frames: []string{"…"}, FPS: time.Hour}
	}
	sp.Style = sAccent
	return sp
}

// spinning advances a spinner while busy.
func spinning(busy bool, sp *spinner.Model, msg tea.Msg) tea.Cmd {
	if !busy {
		return nil
	}
	var cmd tea.Cmd
	*sp, cmd = sp.Update(msg)
	return cmd
}

func cursorKey(k string, cur, n int) int {
	switch k {
	case "up", "k", "shift+tab":
		return max(0, cur-1)
	case "down", "j", "tab":
		return max(0, min(n-1, cur+1))
	case "home", "g":
		return 0
	case "end", "G":
		return max(0, n-1)
	}
	return cur
}

// confirmKey answers a row's confirm prompt: y or enter says yes, and any
// other key cancels it, moving the cursor if it's a move.
func confirmKey(k string, cursor *int, confirm *bool, n int) bool {
	*confirm = false
	if k == "y" || k == "enter" {
		return true
	}
	*cursor = cursorKey(k, *cursor, n)
	return false
}

// listRows renders a scrolled list with the cursor row highlighted.
func listRows(rows []string, cur, h, w int) string {
	start := max(0, min(cur-h/2, len(rows)-h))
	var out []string
	for i := start; i < len(rows) && i < start+h; i++ {
		if i == cur {
			out = append(out, markRow("row:"+strconv.Itoa(i), sAccent.Render("▌")+"  "+rows[i], w))
		} else {
			out = append(out, markRow("row:"+strconv.Itoa(i), "   "+rows[i], w))
		}
	}
	return strings.Join(out, "\n")
}

func errorView(err error) string {
	return sBad.Render("✗ "+err.Error()) + "\n\n" + sMuted.Render("Press ") + keyLink("r") + sMuted.Render(" to try again or esc to go back.")
}

// confirmKeys is the last line of a confirmView: the key that does it
// and esc. y and enter do it too, and so does a key that asked, pressed
// again; Review's deploy alone takes y only, its enter having just asked.
func confirmKeys(key, verb string) string {
	return mark("key:"+key, keyChip(key)+sMuted.Render(" "+verb)) + "   " + mark("key:esc", keyChip("esc")+sMuted.Render(" cancel"))
}

func confirmView(w int, lines ...string) string {
	return sPanel.BorderForeground(cAmber).Padding(0, 2).Width(min(w, maxFormWidth) - 2).Render(strings.Join(lines, "\n"))
}

// ── Updates ────────────────────────────────────────────────────────

type changesMsg struct {
	ch  *deploy.Changes
	err error
}

type updatesScreen struct {
	cfg     *config.Config
	loading bool
	ch      *deploy.Changes
	err     error
	spin    spinner.Model
	vp      viewport.Model
}

func newUpdatesScreen() *updatesScreen {
	return &updatesScreen{spin: newSpinner(), vp: viewport.New(0, 0)}
}

func (s *updatesScreen) tool()        {}
func (s *updatesScreen) name() string { return "Updates" }
func (s *updatesScreen) title() string {
	return "What's new in an update"
}
func (s *updatesScreen) typing() bool { return false }
func (s *updatesScreen) keys() [][2]string {
	if s.loading || s.err != nil {
		return [][2]string{{"r", "check again"}}
	}
	return [][2]string{{"enter", "update now"}, {"d", "dry run"}, {"↑↓", "scroll"}, {"r", "check again"}}
}
func (s *updatesScreen) leave(*config.Config) {}

func (s *updatesScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	return s.check()
}

func (s *updatesScreen) check() tea.Cmd {
	s.loading, s.err, s.ch = true, nil, nil
	cfg := *s.cfg
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		ch, err := deploy.PreviewUpdate(ctx, &cfg)
		return changesMsg{ch, err}
	})
}

func (s *updatesScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case changesMsg:
		s.loading, s.ch, s.err = false, m.ch, m.err
		s.vp.SetContent(s.content())
		s.vp.GotoTop()
		return nil
	case spinner.TickMsg:
		return spinning(s.loading, &s.spin, m)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			if !s.loading {
				return s.check()
			}
		case "d":
			if !s.loading {
				return run(deploy.Options{Action: deploy.ActionDryRun})
			}
		case "enter":
			if !s.loading {
				s.cfg.PinnedCommit = ""
				_ = s.cfg.Save()
				return run(deploy.Options{Action: deploy.ActionInstall})
			}
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return cmd
}

func (s *updatesScreen) content() string {
	ch := s.ch
	if ch == nil {
		return ""
	}
	var lines []string
	if pin := s.cfg.PinnedCommit; pin != "" {
		lines = append(lines, sWarn.Render("! The playbook is held at "+pin[:min(10, len(pin))]+" since a rollback. Updating releases it."), "")
	}
	if ch.From == ch.To {
		lines = append(lines, sGood.Render("✓ The playbook is up to date ")+sFaint.Render("("+ch.From[:10]+")"),
			"", sMuted.Render("Press enter to re-run the install anyway, e.g. to apply changed settings."))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, sTitle.Render(fmt.Sprintf("%d new playbook commit(s)", ch.Total))+
		sFaint.Render(fmt.Sprintf("  %s → %s", ch.From[:10], ch.To[:10])), "")
	if len(ch.Notices) > 0 {
		lines = append(append(lines, sectionHeading("From the playbook's changelog", 60), ""), noticeLines(ch.Notices)...)
	}
	if len(ch.Versions) > 0 {
		lines = append(lines, sectionHeading("Version changes", 60), "")
		nameW := 18
		for _, v := range ch.Versions {
			nameW = max(nameW, min(52, len(v.Name)+2))
		}
		for _, v := range ch.Versions {
			name := lipgloss.NewStyle().Width(nameW).Render(sMuted.Render(v.Name))
			switch {
			case v.Old == "":
				lines = append(lines, "  "+name+sGood.Render("new: "+v.New))
			case v.New == "":
				lines = append(lines, "  "+name+sFaint.Render("removed (was "+v.Old+")"))
			default:
				lines = append(lines, "  "+name+sFaint.Render(v.Old)+sMuted.Render("  →  ")+sGood.Render(v.New))
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, sectionHeading("Commits", 60), "")
	for _, c := range ch.Commits {
		lines = append(lines, "  "+sPink.Render(c.Hash)+"  "+sFaint.Render(c.Date)+"  "+c.Subject)
	}
	if ch.Total > len(ch.Commits) {
		lines = append(lines, sFaint.Render(fmt.Sprintf("  …and %d older", ch.Total-len(ch.Commits))))
	}
	return strings.Join(lines, "\n")
}

// noticeLines lists the changelog entries an update brings: the ones about
// what this server runs first, with their first paragraph, then the rest
// by title.
func noticeLines(ns []deploy.Notice) []string {
	var yours, rest []string
	for _, n := range ns {
		head := sFaint.Render(n.Date) + "  " + sTitle.Render(n.Title)
		if len(n.Yours) == 0 {
			rest = append(rest, "  "+sFaint.Render("·")+" "+head)
			continue
		}
		para, _, _ := strings.Cut(n.Body, "\n\n")
		body := lipgloss.NewStyle().Width(68).Render(strings.Join(strings.Fields(para), " "))
		yours = append(yours, "  "+sWarn.Render("!")+" "+head+sWarn.Render("  ("+strings.Join(n.Yours, ", ")+")"),
			"    "+sMuted.Render(strings.ReplaceAll(body, "\n", "\n    ")), "")
	}
	out := yours
	if len(yours) > 0 {
		out = append([]string{sWarn.Render("! These are about what your server runs; read them before updating."), ""}, out...)
	}
	if len(rest) > 0 {
		out = append(out, rest...)
		out = append(out, "")
	}
	return append(out, sFaint.Render("  The whole changelog: CHANGELOG.md in the playbook."), "")
}

func (s *updatesScreen) view(w, h int) string {
	switch {
	case s.loading:
		return s.spin.View() + sMuted.Render(" fetching the playbook from "+s.cfg.Sources.PlaybookGit+"…")
	case s.err != nil:
		return errorView(s.err)
	}
	s.vp.Width, s.vp.Height = w, h
	return s.vp.View()
}

// ── Rollback ───────────────────────────────────────────────────────

type historyScreen struct {
	cfg     *config.Config
	list    []deploy.Deploy
	cursor  int
	confirm bool
}

func newHistoryScreen() *historyScreen { return &historyScreen{} }

func (s *historyScreen) tool()                {}
func (s *historyScreen) name() string         { return "Rollback" }
func (s *historyScreen) title() string        { return "Roll back to an earlier deploy" }
func (s *historyScreen) typing() bool         { return false }
func (s *historyScreen) handlesEsc() bool     { return s.confirm }
func (s *historyScreen) leave(*config.Config) {}
func (s *historyScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.list, s.cursor, s.confirm = c, deploy.History(), 0, false
	return nil
}
func (s *historyScreen) keys() [][2]string {
	if s.confirm {
		return [][2]string{{"enter", "roll back"}, {"esc", "cancel"}}
	}
	return [][2]string{{"↑↓", "select"}, {"enter", "roll back to this"}}
}

func (s *historyScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok || len(s.list) == 0 {
		return nil
	}
	if s.confirm {
		if confirmKey(k.String(), &s.cursor, &s.confirm, len(s.list)) {
			return s.rollback(s.list[s.cursor])
		}
		return nil
	}
	s.cursor = cursorKey(k.String(), s.cursor, len(s.list))
	if k.String() == "enter" {
		s.confirm = true
	}
	return nil
}

func (s *historyScreen) rollback(d deploy.Deploy) tea.Cmd {
	rc, err := deploy.RollbackConfig(s.cfg, d)
	if err != nil {
		return nil
	}
	// Keep the answers being replaced, in case they're wanted back.
	_ = s.cfg.SaveFile(filepath.Join(config.Dir(), "config.before-rollback.json"))
	*s.cfg = *rc
	_ = s.cfg.Save()
	return run(deploy.Options{Action: deploy.ActionRollback})
}

func (s *historyScreen) view(w, h int) string {
	if len(s.list) == 0 {
		return sMuted.Render("No deploys recorded yet. Every successful install from now on is recorded here,\nwith the playbook commit and your answers, so it can be restored later.")
	}
	var rows []string
	for i, d := range s.list {
		line := sTitle.Render(d.Time.Local().Format("2006-01-02 15:04")) + "  " + sPink.Render(d.Commit[:min(10, len(d.Commit))]) + "  " +
			sMuted.Render(truncate(d.Subject, max(10, w-60))) + sFaint.Render(fmt.Sprintf("  %s · %d components", d.Homeserver, d.Components))
		if i == 0 {
			line += sGood.Render("  latest")
		}
		rows = append(rows, line)
	}
	out := listRows(rows, s.cursor, h-10, w)
	if s.confirm {
		d := s.list[s.cursor]
		out += "\n\n" + confirmView(w,
			sWarn.Render("Roll back to "+d.Time.Local().Format("2006-01-02 15:04")+"?"),
			sSub.Render("The playbook goes back to "+d.Commit[:min(10, len(d.Commit))]+" and your answers to the ones used then (today's are kept in config.before-rollback.json). The database is backed up first."),
			sSub.Render("Newer homeserver versions may have changed the database in ways an older one can't read; if it fails to start, restore the backup taken now with Manage › Restore."),
			"", confirmKeys("enter", "roll back"))
	}
	return out
}

// ── Restore ────────────────────────────────────────────────────────

type restoreScreen struct {
	cfg     *config.Config
	list    []deploy.LocalBackup
	cursor  int
	confirm bool
}

func newRestoreScreen() *restoreScreen { return &restoreScreen{} }

func (s *restoreScreen) tool()                {}
func (s *restoreScreen) name() string         { return "Restore" }
func (s *restoreScreen) title() string        { return "Restore a database backup" }
func (s *restoreScreen) typing() bool         { return false }
func (s *restoreScreen) handlesEsc() bool     { return s.confirm }
func (s *restoreScreen) leave(*config.Config) {}
func (s *restoreScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.list, s.cursor, s.confirm = c, deploy.ListBackups(c.Domain), 0, false
	return nil
}
func (s *restoreScreen) keys() [][2]string {
	if s.confirm {
		return [][2]string{{"enter", "restore"}, {"esc", "cancel"}}
	}
	return [][2]string{{"↑↓", "select"}, {"enter", "restore this"}, {"d", "download the server's backups"}}
}

func (s *restoreScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.confirm {
		if confirmKey(k.String(), &s.cursor, &s.confirm, len(s.list)) {
			return run(deploy.Options{Action: deploy.ActionRestore, Backup: s.list[s.cursor].Path})
		}
		return nil
	}
	switch k.String() {
	case "d":
		return run(deploy.Options{Action: deploy.ActionFetchBackups})
	case "enter":
		if len(s.list) > 0 {
			s.confirm = true
		}
	default:
		s.cursor = cursorKey(k.String(), s.cursor, len(s.list))
	}
	return nil
}

func (s *restoreScreen) view(w, h int) string {
	if s.cfg.Components["external-postgres"] {
		return sMuted.Render("You use an external Postgres server; restore it where it runs.")
	}
	if len(s.list) == 0 {
		return sMuted.Render("No backups of "+s.cfg.Domain+" in "+deploy.BackupDir()+".") + "\n\n" +
			sMuted.Render("Make one with Manage › Back up the database, or press ") + keyLink("d") +
			sMuted.Render(" to download the server's scheduled backups.")
	}
	var rows []string
	for _, b := range s.list {
		rows = append(rows, sTitle.Render(b.Time.Local().Format("2006-01-02 15:04"))+"  "+
			lipgloss.NewStyle().Width(10).Render(sMuted.Render(deploy.HumanSize(b.Size)))+sFaint.Render(filepath.Base(b.Path)))
	}
	out := sMuted.Render(deploy.BackupDir()) + "\n\n" + listRows(rows, s.cursor, h-12, w)
	if s.confirm {
		b := s.list[s.cursor]
		out += "\n\n" + confirmView(w,
			sWarn.Render("Replace the database of "+s.cfg.Domain+" with the dump from "+b.Time.Local().Format("2006-01-02 15:04")+"?"),
			sSub.Render("Every service stops, the current database is dumped to "+deploy.BackupDir()+" first, then emptied and loaded from this file. Messages sent since the dump are lost."),
			"", confirmKeys("enter", "restore"))
	}
	return out
}

// ── Scheduled backups ──────────────────────────────────────────────

type backupsScreen struct {
	cfg  *config.Config
	form *form
	err  string
}

func newBackupsScreen() *backupsScreen { return &backupsScreen{} }

func (s *backupsScreen) tool()                {}
func (s *backupsScreen) name() string         { return "Backups" }
func (s *backupsScreen) title() string        { return "Scheduled backups" }
func (s *backupsScreen) typing() bool         { return s.form.typing() }
func (s *backupsScreen) leave(*config.Config) {}
func (s *backupsScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"space", "toggle"}, {"enter", "apply on the server"}, {"ctrl+d", "download backups now"}}
}

func (s *backupsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.err = c, ""
	b := c.Backups
	if b.When == "" {
		b.When = "daily"
	}
	if b.Keep == 0 {
		b.Keep = 7
	}
	s.form = newForm(
		toggleField("on", "Back up the database on a schedule", b.Enabled,
			"The server dumps its Postgres database into "+deploy.ServerBackupDir+" by itself, even when this computer is off."),
		textField("when", "When", b.When, "daily",
			"A systemd calendar time: daily, weekly, hourly, Mon *-*-* 04:00, *-*-* 03:30 …"),
		textField("keep", "Backups to keep", strconv.Itoa(b.Keep), "7", "Older dumps are deleted after each new one."),
	)
	off := func() bool { return !s.form.get("on").on }
	s.form.get("when").hidden, s.form.get("keep").hidden = off, off
	return nil
}

func (s *backupsScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case formDoneMsg:
		return s.apply()
	case tea.KeyMsg:
		if m.String() == "ctrl+d" {
			return run(deploy.Options{Action: deploy.ActionFetchBackups})
		}
	}
	return s.form.Update(msg)
}

func (s *backupsScreen) apply() tea.Cmd {
	keep, err := strconv.Atoi(s.form.value("keep"))
	if s.form.get("on").on && (err != nil || keep < 1) {
		s.err = "Backups to keep must be a number of at least 1."
		return nil
	}
	s.cfg.Backups = config.BackupSchedule{Enabled: s.form.get("on").on, When: s.form.value("when"), Keep: max(keep, 1)}
	_ = s.cfg.Save()
	return run(deploy.Options{Action: deploy.ActionSchedule})
}

func (s *backupsScreen) view(w, h int) string {
	info := []string{
		sSub.Render("Each scheduled backup is a full pg_dumpall, the same as Manage › Back up the database."),
		sSub.Render("Press ") + keyLink("ctrl+d") + sSub.Render(" to copy them to ") + sTitle.Render(deploy.BackupDir()) +
			sSub.Render("; Manage › Restore lists them there."),
	}
	if n := len(deploy.ListBackups(s.cfg.Domain)); n > 0 {
		info = append(info, sMuted.Render(fmt.Sprintf("%d backup(s) of %s on this computer.", n, s.cfg.Domain)))
	}
	panel := sPanel.Padding(0, 2).Width(min(w, maxFormWidth) - 2).Render(strings.Join(info, "\n"))
	out := s.form.View(w, h-lipgloss.Height(panel)-3) + "\n\n" + panel
	if s.err != "" {
		out += "\n" + sBad.Render("✗ "+s.err)
	}
	return out
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ── Servers ────────────────────────────────────────────────────────

type serversScreen struct {
	cfg     *config.Config
	list    []config.Profile
	cursor  int
	form    *form // naming a new server
	confirm bool  // forgetting one
	reset   bool  // asking before starting from nothing
	err     string
	imp     *importState // taking over a server set up by hand (import.go)
}

func newServersScreen() *serversScreen { return &serversScreen{} }

func (s *serversScreen) tool()        {}
func (s *serversScreen) name() string { return "Servers" }
func (s *serversScreen) title() string {
	if s.imp != nil {
		return "Import a server"
	}
	return "Your servers"
}
func (s *serversScreen) typing() bool {
	if s.imp != nil {
		return s.imp.im == nil && s.imp.form.typing()
	}
	return s.form != nil && s.form.typing()
}
func (s *serversScreen) handlesEsc() bool {
	return s.form != nil || s.confirm || s.reset || s.imp != nil
}
func (s *serversScreen) leave(*config.Config) {}
func (s *serversScreen) keys() [][2]string {
	switch {
	case s.imp != nil:
		return s.importKeys()
	case s.form != nil:
		return [][2]string{{"enter", "set up this server"}, {"esc", "cancel"}}
	case s.confirm:
		return [][2]string{{"x", "forget it"}, {"esc", "cancel"}}
	case s.reset:
		return [][2]string{{"R", "start fresh"}, {"esc", "cancel"}}
	}
	return [][2]string{{"↑↓", "select"}, {"enter", "switch to it"}, {"n", "add a server"}, {"i", "import one"}, {"x", "forget"}, {"R", "start fresh"}}
}

func (s *serversScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.form, s.confirm, s.reset, s.err, s.imp = c, nil, false, false, "", nil
	s.list = config.Profiles()
	if len(s.list) == 0 { // nothing saved yet: the answers so far are the default server
		s.list = []config.Profile{{Name: config.DefaultProfile, Domain: c.Domain}}
	}
	for i, p := range s.list {
		if p.Name == config.Active() {
			s.cursor = i
		}
	}
	return nil
}

func (s *serversScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(importOpenMsg); ok {
		s.startImport()
		return nil
	}
	if s.imp != nil {
		return s.importUpdate(msg)
	}
	if _, ok := msg.(formDoneMsg); ok && s.form != nil {
		return s.create()
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.form != nil {
		if k.String() == "esc" {
			s.form = nil
			return nil
		}
		return s.form.Update(msg)
	}
	if s.confirm {
		if confirmKey(k.String(), &s.cursor, &s.confirm, len(s.list)) || k.String() == "x" {
			if err := config.RemoveProfile(s.list[s.cursor].Name); err != nil {
				s.err = err.Error()
			}
			return s.enter(s.cfg)
		}
		return nil
	}
	if s.reset {
		s.reset = false
		if k := k.String(); k != "R" && k != "enter" && k != "y" {
			return nil
		}
		moved, err := config.Reset()
		if err != nil {
			s.err = err.Error()
			return nil
		}
		note := "✓ starting fresh"
		if len(moved) > 0 {
			note += "; the old setup is in " + strings.Join(moved, " and ")
		}
		return func() tea.Msg {
			return switchServerMsg{name: config.DefaultProfile, cfg: config.Fresh(), fresh: true, note: note}
		}
	}
	s.err = ""
	switch k.String() {
	case "R":
		s.reset = true
	case "enter":
		p := s.list[s.cursor]
		if p.Name == config.Active() {
			return func() tea.Msg { return gotoMsg("Manage") }
		}
		c, err := config.LoadFile(config.ProfilePath(p.Name))
		if err != nil {
			s.err = err.Error()
			return nil
		}
		return func() tea.Msg { return switchServerMsg{name: p.Name, cfg: c} }
	case "n":
		f := textField("name", "A short name for the new server", "", "home, work, test-server",
			"Lowercase letters, digits, dots and dashes. It names the saved answers; the wizard asks for the domain next.")
		f.required = true
		s.form = newForm(f)
	case "i":
		s.startImport()
	case "x":
		p := s.list[s.cursor]
		switch {
		case p.Name == config.DefaultProfile:
			s.err = "the first server can't be forgotten"
		case p.Name == config.Active():
			s.err = "switch to another server first"
		default:
			s.confirm = true
		}
	default:
		s.cursor = cursorKey(k.String(), s.cursor, len(s.list))
	}
	return nil
}

func (s *serversScreen) create() tea.Cmd {
	name := strings.ToLower(s.form.value("name"))
	if err := config.ValidProfileName(name); err != nil {
		s.err = err.Error()
		return nil
	}
	if _, err := os.Stat(config.ProfilePath(name)); err == nil || name == config.DefaultProfile {
		s.err = "there is already a server called " + name
		return nil
	}
	c := config.NewProfile(name, s.cfg)
	if err := c.SaveFile(config.ProfilePath(name)); err != nil {
		s.err = err.Error()
		return nil
	}
	s.form = nil
	return func() tea.Msg { return switchServerMsg{name: name, cfg: c, fresh: true} }
}

func (s *serversScreen) view(w, h int) string {
	if s.imp != nil {
		return s.importView(w, h)
	}
	if s.form != nil {
		out := s.form.View(w, h-2)
		if s.err != "" {
			out += "\n\n" + sBad.Render("✗ "+s.err)
		}
		return out
	}
	var rows []string
	for _, p := range s.list {
		domain := p.Domain
		if domain == "" {
			domain = "(not set up yet)"
		}
		line := lipgloss.NewStyle().Width(20).Render(sTitle.Render(p.Name)) +
			lipgloss.NewStyle().Width(28).Render(sSub.Render(domain)) + sFaint.Render(p.Host)
		if p.Name == config.Active() {
			line += sGood.Render("  ● active")
		}
		rows = append(rows, line)
	}
	out := listRows(rows, s.cursor, h-8, w) + "\n\n" +
		sFaint.Render("Each server has its own answers and playbook copy. Commands use the active one;\nINTERZONE_SERVER=name picks another for a single run.")
	if s.confirm {
		out += "\n\n" + confirmView(w, sWarn.Render("Forget "+s.list[s.cursor].Name+"?"),
			sSub.Render("Only interzone's saved answers are deleted. Nothing changes on the server itself."), "",
			confirmKeys("x", "forget"))
	}
	if s.reset {
		out += "\n\n" + confirmView(w, sWarn.Render("Start interzone fresh?"),
			sSub.Render("interzone forgets every server here: answers, playbook copies, history, downloaded backups, theme and layout. "+
				"Nothing is deleted: its folders are moved aside, so they can be put back. "+
				"The servers keep running; to manage one again, import it under Servers."), "",
			confirmKeys("R", "start fresh"))
	}
	if s.err != "" {
		out += "\n\n" + sBad.Render("✗ "+s.err)
	}
	return out
}

// ── Migrate ────────────────────────────────────────────────────────

type migrateScreen struct {
	cfg     *config.Config
	form    *form
	confirm bool
}

func newMigrateScreen() *migrateScreen { return &migrateScreen{} }

func (s *migrateScreen) tool()                {}
func (s *migrateScreen) name() string         { return "Move" }
func (s *migrateScreen) title() string        { return "Move " + s.cfg.Domain + " to another server" }
func (s *migrateScreen) typing() bool         { return !s.confirm && s.form.typing() }
func (s *migrateScreen) handlesEsc() bool     { return s.confirm }
func (s *migrateScreen) leave(*config.Config) {}
func (s *migrateScreen) keys() [][2]string {
	if s.confirm {
		return [][2]string{{"y", "start the move"}, {"esc", "back to the form"}}
	}
	return [][2]string{{"↑↓", "move"}, {"←→", "choose"}, {"ctrl+r", "reveal"}, {"enter", "next"}}
}

func (s *migrateScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.confirm = c, false
	host := textField("host", "New server address", "", "203.0.113.20 or new.example.com", "IP or hostname reachable over SSH from this computer.")
	host.required = true
	f := newForm(
		choiceField("mode", "The new server is", []string{"ssh", "local"}, []string{"A remote server over SSH", "This machine"}, "ssh", ""),
		host,
		textField("user", "SSH user", "root", "root", ""),
		textField("port", "SSH port", "22", "22", ""),
		textField("key", "SSH private key", c.Target.SSHKey, "~/.ssh/id_ed25519", "Leave empty to use your SSH agent or default key."),
		secretField("become", "sudo password", "", "Needed if the user isn't root and sudo asks for a password."),
	)
	remote := func() bool { return f.value("mode") != "ssh" }
	for _, k := range []string{"host", "user", "port", "key"} {
		f.get(k).hidden = remote
	}
	s.form = f
	return nil
}

func (s *migrateScreen) target() config.Target {
	f := s.form
	t := config.Target{Mode: f.value("mode"), Host: f.value("host"), User: f.value("user"),
		Port: f.value("port"), SSHKey: f.value("key"), BecomePass: f.value("become")}
	if t.User == "" {
		t.User = "root"
	}
	return t
}

func (s *migrateScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(formDoneMsg); ok {
		if t := s.target(); t.Mode == "local" || t.Host != "" {
			s.confirm = true
		}
		return nil
	}
	if k, ok := msg.(tea.KeyMsg); ok && s.confirm {
		switch k.String() {
		case "y":
			s.confirm = false
			return run(deploy.Options{Action: deploy.ActionMigrate, NewTarget: s.target()})
		case "esc", "n":
			s.confirm = false
		}
		return nil
	}
	return s.form.Update(msg)
}

func (s *migrateScreen) view(w, h int) string {
	from := "this machine"
	if s.cfg.Target.Mode == "ssh" {
		from = s.cfg.Target.User + "@" + s.cfg.Target.Host
	}
	info := sPanel.Padding(0, 2).Width(min(w, maxFormWidth) - 2).Render(strings.Join([]string{
		sSub.Render("Moves everything from " + from + ": the database, media, bridges' data and keys, by copying /matrix."),
		sSub.Render("Both servers must have the same CPU architecture, and the new one must not have a /matrix yet."),
		sSub.Render("The old server's services are stopped and disabled, but its files stay until you delete them."),
	}, "\n"))
	if s.confirm {
		t := s.target()
		to := "this machine"
		if t.Mode == "ssh" {
			to = t.User + "@" + t.Host
		}
		return info + "\n\n" + confirmView(w,
			sWarn.Render("Move "+s.cfg.Domain+" from "+from+" to "+to+"?"),
			sSub.Render("Matrix is down from the moment the old server stops until the new one is installed. Afterwards, point these DNS records at the new server: "+
				hostList(s.cfg)+"."),
			"", keyLink("y")+sMuted.Render(" start   ")+keyLink("esc")+sMuted.Render(" back"))
	}
	return s.form.View(w, h-lipgloss.Height(info)-3) + "\n\n" + info
}
