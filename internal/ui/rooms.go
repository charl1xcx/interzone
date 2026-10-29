package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Rooms ──────────────────────────────────────────────────────────
//
// Every room Synapse knows of, biggest first, with who's in it. A room can
// be blocked, deleted from the server, or have its old history deleted.
// It signs in with the admin account the Users screen uses.

type roomsMsg struct {
	server string
	gen    int
	list   []deploy.Room
	token  string
	err    error
}

func (roomsMsg) owner() string { return "Rooms" }

type roomOpMsg struct {
	info, token string
	err         error
}

func (roomOpMsg) owner() string { return "Rooms" }

type roomsScreen struct {
	cfg     *config.Config
	server  string
	list    []deploy.Room
	loaded  bool
	cursor  int
	form    *form  // how much history to delete
	confirm string // "delete" or "block", for the room under the cursor
	narrow  bool

	busy bool
	gen  int
	err  error
	info string
	spin spinner.Model
}

func newRoomsScreen() *roomsScreen { return &roomsScreen{spin: newSpinner()} }

func (s *roomsScreen) tool()                {}
func (s *roomsScreen) name() string         { return "Rooms" }
func (s *roomsScreen) title() string        { return "Rooms on " + s.cfg.Domain }
func (s *roomsScreen) typing() bool         { return s.form != nil && s.form.typing() }
func (s *roomsScreen) handlesEsc() bool     { return s.form != nil || s.confirm != "" }
func (s *roomsScreen) leave(*config.Config) {}
func (s *roomsScreen) setCompact(on bool)   { s.narrow = on }
func (s *roomsScreen) activeForm() *form    { return s.form }

func (s *roomsScreen) keys() [][2]string {
	switch {
	case s.form != nil && s.confirm == "delete":
		return [][2]string{{"enter", "delete the room"}, {"esc", "cancel"}}
	case s.form != nil:
		return [][2]string{{"enter", "delete history"}, {"esc", "cancel"}}
	case s.confirm != "":
		return [][2]string{{"enter", "do it"}, {"esc", "cancel"}}
	}
	keys := [][2]string{{"↑↓", "select"}}
	if r, ok := s.current(); ok {
		keys = append(keys, [2]string{"b", map[bool]string{true: "unblock", false: "block"}[r.Blocked]},
			[2]string{"h", "delete old history"}, [2]string{"x", "delete room"})
	}
	return append(keys, [2]string{"r", "reload"})
}

func (s *roomsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.form, s.confirm, s.info = nil, "", ""
	if srv := config.Active(); srv != s.server {
		s.server, s.list, s.loaded, s.cursor = srv, nil, false, 0
	}
	if c.Homeserver != "synapse" {
		return nil
	}
	return s.reload()
}

func (s *roomsScreen) login() deploy.AdminLogin {
	return deploy.AdminLogin{User: s.cfg.AdminUser, Password: s.cfg.AdminPassword, Token: s.cfg.AdminToken}
}

// saveToken keeps the session the admin script opened, as Users does.
func (s *roomsScreen) saveToken(t string) {
	if t != "" && s.cfg.AdminToken != t {
		s.cfg.AdminToken = t
		_ = s.cfg.Save()
	}
}

func (s *roomsScreen) reload() tea.Cmd {
	s.busy, s.err = true, nil
	s.gen++
	cfg, login, srv, gen := *s.cfg, s.login(), s.server, s.gen
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		list, err := deploy.ListRooms(ctx, &cfg, &login)
		return roomsMsg{srv, gen, list, login.Token, err}
	})
}

// do runs a change, then reloads the list.
func (s *roomsScreen) do(info string, fn func(ctx context.Context, c *config.Config, login *deploy.AdminLogin) error) tea.Cmd {
	s.busy, s.err, s.info = true, nil, ""
	cfg, login := *s.cfg, s.login()
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err := fn(ctx, &cfg, &login)
		return roomOpMsg{info, login.Token, err}
	})
}

func (s *roomsScreen) current() (deploy.Room, bool) {
	if s.cursor < 0 || s.cursor >= len(s.list) {
		return deploy.Room{}, false
	}
	return s.list[s.cursor], true
}

// purge deletes the history the form asks for.
func (s *roomsScreen) purge() tea.Cmd {
	r, ok := s.current()
	days, err := strconv.Atoi(strings.TrimSpace(s.form.value("days")))
	if err != nil || days < 1 {
		s.err = fmt.Errorf("days should be a whole number above 0")
		return nil
	}
	s.form = nil
	if !ok {
		return nil
	}
	before := time.Now().AddDate(0, 0, -days)
	return s.do(fmt.Sprintf("messages older than %d days are being deleted from %s", days, r.Title()),
		func(ctx context.Context, c *config.Config, login *deploy.AdminLogin) error {
			return deploy.PurgeHistory(ctx, c, login, r.ID, before)
		})
}

func (s *roomsScreen) deleteRoom() tea.Cmd {
	r, ok := s.current()
	if strings.TrimSpace(strings.ToLower(s.form.value("sure"))) != "delete" {
		s.err = fmt.Errorf("type the word delete, or press esc to keep the room")
		return nil
	}
	s.form, s.confirm = nil, ""
	if !ok {
		return nil
	}
	return s.do(r.Title()+" is being deleted", func(ctx context.Context, c *config.Config, login *deploy.AdminLogin) error {
		return deploy.DeleteRoom(ctx, c, login, r.ID)
	})
}

func (s *roomsScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case roomsMsg:
		if m.server != s.server || m.gen != s.gen {
			return nil
		}
		s.busy, s.err = false, m.err
		s.saveToken(m.token)
		if m.err == nil {
			s.list, s.loaded = m.list, true
			s.cursor = min(s.cursor, max(0, len(s.list)-1))
		}
		return nil
	case roomOpMsg:
		s.busy = false
		s.saveToken(m.token)
		if m.err != nil {
			s.err = m.err
			return nil
		}
		s.info = "✓ " + m.info
		return s.reload()
	case spinner.TickMsg:
		return spinning(s.busy, &s.spin, m)
	case formDoneMsg:
		if s.form != nil && s.confirm == "delete" {
			return s.deleteRoom()
		}
		if s.form != nil {
			return s.purge()
		}
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.form != nil {
		if k.String() == "esc" {
			s.form, s.err, s.confirm = nil, nil, ""
			return nil
		}
		return s.form.Update(msg)
	}
	if s.confirm != "" {
		r, ok := s.current()
		switch k.String() {
		case "enter", "y":
			s.confirm = ""
			if !ok {
				return nil
			}
			info := map[bool]string{true: r.Title() + " is blocked", false: r.Title() + " can be joined again"}[!r.Blocked]
			return s.do(info, func(ctx context.Context, c *config.Config, login *deploy.AdminLogin) error {
				return deploy.BlockRoom(ctx, c, login, r.ID, !r.Blocked)
			})
		case "esc", "n":
			s.confirm = ""
		default:
			if next := cursorKey(k.String(), s.cursor, len(s.list)); next != s.cursor { // moving away cancels
				s.confirm, s.cursor = "", next
			}
		}
		return nil
	}
	s.cursor = cursorKey(k.String(), s.cursor, len(s.list))
	if s.busy || s.cfg.Homeserver != "synapse" {
		return nil
	}
	_, ok = s.current()
	switch k.String() {
	case "r":
		return s.reload()
	case "b":
		if ok {
			s.confirm = "block"
		}
	case "x": // can't be undone: the word is typed, not a key pressed twice
		if ok {
			s.err, s.info = nil, ""
			sure := textField("sure", "Type delete to confirm", "", "", "")
			sure.required = true
			s.form, s.confirm = newForm(sure), "delete"
		}
	case "h":
		if ok {
			s.err, s.info = nil, ""
			days := textField("days", "Older than", "180", "180", "Days. Messages sent from this server stay, as other servers may not have them; the rest are deleted from this server's database.")
			days.required = true
			s.form = newForm(days)
		}
	}
	return nil
}

func (s *roomsScreen) click(id string) (tea.Cmd, bool) {
	if s.form != nil || s.confirm != "" || s.busy {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.list), s.update)
}

func (s *roomsScreen) view(w, h int) string {
	if s.cfg.Homeserver != "synapse" {
		return sMuted.Width(min(w, 70)).Render("Room admin uses Synapse's admin API, and this server runs " + homeserverName(s.cfg.Homeserver) + ".")
	}
	if s.form != nil {
		r, _ := s.current()
		head := sSub.Render("Delete old messages in "+r.Title()) + "\n"
		if s.confirm == "delete" {
			head = confirmView(w, sWarn.Render("Delete "+r.Title()+"?"),
				sSub.Render(fmt.Sprintf("Its %d people on this server are made to leave, its messages are removed from this server, and it's blocked so it can't be joined again. This can't be undone.", r.Local))) + "\n"
		}
		if s.err != nil {
			head += sBad.Render("✗ "+s.err.Error()) + "\n"
		}
		return head + "\n" + s.form.View(w, h-3)
	}
	foot := s.footer(w)
	if !s.loaded {
		return foot
	}
	top := sMuted.Render(fmt.Sprintf("%d rooms, biggest first", len(s.list)))
	listH := max(3, h-lipgloss.Height(top)-lipgloss.Height(foot)-4)
	var body string
	switch {
	case len(s.list) == 0:
		body = sMuted.Render("The server isn't in any rooms yet.")
	case w >= 84 && !s.narrow:
		lw := min(56, w*9/20)
		card := ""
		if r, ok := s.current(); ok {
			card = s.card(r, w-lw-2)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(lw+2).Render(s.listView(lw, listH, false)), card)
	default:
		body = s.listView(w, listH, true)
	}
	out := top + "\n\n" + body
	if foot != "" {
		out += "\n\n" + foot
	}
	return out
}

func (s *roomsScreen) footer(w int) string {
	var status string
	switch {
	case s.busy:
		status = s.spin.View() + sMuted.Render(" talking to Synapse on the server…")
	case s.err != nil:
		status = sBad.Render("✗ " + s.err.Error())
		if _, bad := s.err.(deploy.ErrAdminLogin); bad || s.cfg.AdminUser == "" {
			status += "\n" + sMuted.Render("Sign in on Users first; Rooms uses the same admin account.")
		}
	case s.info != "":
		status = sGood.Render(s.info)
	}
	r, ok := s.current()
	if s.confirm == "" || !ok {
		return status
	}
	if r.Blocked {
		return confirmView(w, sWarn.Render("Unblock "+r.Title()+"?"), "", confirmKeys("enter", "unblock"))
	}
	return confirmView(w, sWarn.Render("Block "+r.Title()+"?"),
		sSub.Render("Nobody on this server can join it any more; who's in it stays."), "", confirmKeys("enter", "block"))
}

func (s *roomsScreen) listView(w, h int, inline bool) string {
	var lines []string
	cur := 0
	for i, r := range s.list {
		id := "row:" + fmt.Sprint(i)
		if i == s.cursor {
			cur = len(lines)
			if inline && !tight {
				lines = append(lines, markLines(id, s.card(r, min(w, maxFormWidth))))
				continue
			}
			lines = append(lines, markRow(id, sAccent.Render("▌")+" "+s.rowLine(r, w-2, true), w))
			continue
		}
		lines = append(lines, markRow(id, "  "+s.rowLine(r, w-2, false), w))
	}
	return windowAround(strings.Join(lines, "\n"), cur, h)
}

func roomDot(r deploy.Room) string {
	if r.Blocked {
		return sFaint.Render("⊘")
	}
	return sGood.Render("#")
}

func (s *roomsScreen) rowLine(r deploy.Room, w int, on bool) string {
	name := lipgloss.NewStyle().Foreground(cText)
	if on {
		name = sTitle
	}
	if r.Blocked {
		name = sFaint
	}
	right := sMuted.Render(fmt.Sprintf("%d members", r.Members))
	room := w - 3 - lipgloss.Width(right) - 2
	left := roomDot(r) + "  " + name.Render(truncate(r.Title(), max(6, room)))
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func (s *roomsScreen) card(r deploy.Room, w int) string {
	inner := w - 4
	head := roomDot(r) + "  " + gradient(truncate(r.Title(), inner-4))
	if r.Blocked {
		head = roomDot(r) + "  " + sFaint.Render(truncate(r.Title(), inner-14)) + "  " + tagChip("blocked", cAmber)
	}
	lines := []string{head}
	if r.Alias != "" && r.Name != "" {
		lines = append(lines, "   "+sMuted.Render(truncate(r.Alias, inner-3)))
	}
	lines = append(lines, "")
	kv := func(k, v string) { lines = append(lines, sMuted.Render(fmt.Sprintf("%-11s", k))+v) }
	kv("Members", sTitle.Render(fmt.Sprint(r.Members))+sMuted.Render(fmt.Sprintf("  %d from here", r.Local)))
	kv("Size", sTitle.Render(fmt.Sprint(r.Events))+sMuted.Render(" state events"))
	var tags []string
	for _, t := range []struct {
		on   bool
		name string
	}{{r.Public, "public"}, {r.Encrypted, "encrypted"}, {!r.Federated, "this server only"}} {
		if t.on {
			tags = append(tags, t.name)
		}
	}
	if len(tags) > 0 {
		kv("", sMuted.Render(strings.Join(tags, " · ")))
	}
	if r.Creator != "" {
		kv("Made by", sTitle.Render(truncate(r.Creator, inner-11)))
	}
	kv("ID", sFaint.Render(truncate(r.ID, inner-11)))
	lines = append(lines, "")
	block := map[bool]string{true: "unblock", false: "block"}[r.Blocked]
	lines = append(lines, mark("key:b", keyChip("b")+" "+sMuted.Render(block))+"   "+
		mark("key:h", keyChip("h")+" "+sMuted.Render("old history"))+"   "+mark("key:x", keyChip("x")+" "+sMuted.Render("delete")))
	return picked(lines, w)
}
