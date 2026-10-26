package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Users ──────────────────────────────────────────────────────────
//
// Everyone on the server: a strip of totals, the people with what they
// use, and the bridges' accounts folded into one group per bridge. The
// person under the cursor opens into a card with their details and what
// can be done to their account; beside the list on a wide terminal, in
// its place on a phone.

type usersMsg struct {
	server string
	list   *deploy.UserList
	token  string
	err    error
}

type userOpMsg struct {
	info     string
	password string // shown once after a reset or a new account
	token    string
	err      error
}

type userMode int

const (
	usersList userMode = iota
	usersLogin
	usersConfirm
	usersNew
)

type userOp struct {
	desc     string
	user     string
	body     map[string]any
	password string
	signOut  bool // end every session instead of changing the account
}

// userFilters and userSorts are what f and o step through.
var (
	userFilters = []string{"everyone", "admins", "active", "inactive", "deactivated"}
	userSorts   = []string{"last seen", "name", "joined", "media"}
)

// activeFor is how recently someone must have been seen to count as active.
const activeFor = 30 * 24 * time.Hour

type usersScreen struct {
	cfg    *config.Config
	server string // whose users are listed
	login  deploy.AdminLogin
	list   *deploy.UserList
	cursor int
	mode   userMode
	form   *form
	op     *userOp

	search    textinput.Model
	searching bool
	filter    int
	sortBy    int
	open      map[string]bool // bridges whose accounts are shown
	narrow    bool

	busy     bool
	err      error
	info     string
	password string
	spin     spinner.Model
}

func newUsersScreen() *usersScreen {
	in := textinput.New()
	in.Prompt = "/ "
	in.PromptStyle = sAccent
	in.Placeholder = "name or @id…"
	in.PlaceholderStyle = sFaint
	return &usersScreen{spin: newSpinner(), search: in, open: map[string]bool{}}
}

func (s *usersScreen) tool()         {}
func (s *usersScreen) name() string  { return "Users" }
func (s *usersScreen) title() string { return "Users on " + s.cfg.Domain }
func (s *usersScreen) typing() bool {
	return s.searching || s.form != nil && (s.mode == usersLogin || s.mode == usersNew) && s.form.typing()
}
func (s *usersScreen) handlesEsc() bool {
	return s.mode != usersList || s.searching || s.search.Value() != ""
}
func (s *usersScreen) leave(*config.Config) {}
func (s *usersScreen) setCompact(on bool)   { s.narrow = on }
func (s *usersScreen) keys() [][2]string {
	switch {
	case s.mode == usersLogin:
		return [][2]string{{"tab/↑↓", "move"}, {"ctrl+r", "reveal"}, {"enter", "sign in"}, {"esc", "cancel"}}
	case s.mode == usersNew:
		return [][2]string{{"tab/↑↓", "move"}, {"space", "toggle"}, {"ctrl+r", "reveal"}, {"enter", "create"}, {"esc", "cancel"}}
	case s.mode == usersConfirm:
		return [][2]string{{"enter", "do it"}, {"esc", "cancel"}}
	case s.searching:
		return [][2]string{{"enter", "done"}, {"esc", "clear"}}
	}
	keys := [][2]string{{"↑↓", "select"}}
	if r, ok := s.current(); ok && r.u == nil {
		keys = append(keys, [2]string{"enter", map[bool]string{true: "hide", false: "show"}[s.open[r.bridge]]})
	}
	keys = append(keys, [2]string{"/", "search"}, [2]string{"f", "filter"}, [2]string{"o", "sort"}, [2]string{"n", "new"})
	if r, ok := s.current(); ok && r.u != nil && r.u.Person() {
		keys = append(keys, [2]string{"p", "password"}, [2]string{"a", "admin"},
			[2]string{"d", map[bool]string{true: "reactivate", false: "deactivate"}[r.u.Deactivated]}, [2]string{"s", "sign out"})
	}
	if s.search.Value() != "" {
		keys = append(keys, [2]string{"esc", "clear search"})
	}
	return append(keys, [2]string{"r", "reload"})
}

func (s *usersScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.login = deploy.AdminLogin{User: c.AdminUser, Password: c.AdminPassword, Token: c.AdminToken}
	s.mode, s.info, s.password, s.err = usersList, "", "", nil
	if srv := config.Active(); srv != s.server {
		s.server, s.list, s.cursor, s.busy = srv, nil, 0, false
		s.open = map[string]bool{}
		if list, _, ok := loadCache[deploy.UserList]("userlist"); ok {
			s.list = &list // shown while the fresh list loads
		}
	}
	return s.reload()
}

func (s *usersScreen) reload() tea.Cmd {
	s.busy, s.err = true, nil
	cfg, login, srv := *s.cfg, s.login, s.server
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		list, err := deploy.ListUsers(ctx, &cfg, &login)
		return usersMsg{srv, list, login.Token, err}
	})
}

func (s *usersScreen) saveToken(t string) {
	s.login.Token = t
	if s.cfg.AdminToken != t {
		s.cfg.AdminToken = t
		_ = s.cfg.Save()
	}
}

func (s *usersScreen) loginForm() {
	if deploy.UsesMAS(s.cfg) {
		u := textField("user", "Admin username", s.login.User, "admin",
			"Your account on "+s.cfg.Domain+". Accounts live in MAS here, so no password is needed: interzone gets an admin session from MAS on the server.")
		s.form, s.mode = newForm(u), usersLogin
		return
	}
	u := textField("user", "Admin username", s.login.User, "admin", "An account with server-admin rights on "+s.cfg.Domain+".")
	pw := secretField("pass", "Password", "", "Only kept until interzone exits; the session it opens is saved.")
	s.form, s.mode = newForm(u, pw), usersLogin
}

func (s *usersScreen) run(op *userOp) tea.Cmd {
	s.busy, s.info, s.password, s.err = true, "", "", nil
	cfg, login := *s.cfg, s.login
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var err error
		if op.signOut {
			err = deploy.SignOutEverywhere(ctx, &cfg, &login, op.user)
		} else {
			err = deploy.UpdateUser(ctx, &cfg, &login, op.user, op.body)
		}
		return userOpMsg{info: op.desc, password: op.password, token: login.Token, err: err}
	})
}

// userRow is a line of the list: a person or a bridge's account, or the
// heading of a bridge's group (u is nil, n is how many it has).
type userRow struct {
	u      *deploy.MatrixUser
	bridge string
	n      int
}

// rows is the list as shown: the people matching the search and filter,
// sorted, then a group per bridge, open when asked or while searching.
func (s *usersScreen) rows() []userRow {
	if s.list == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(s.search.Value()))
	var people []*deploy.MatrixUser
	bridges := map[string][]*deploy.MatrixUser{}
	for i := range s.list.Users {
		u := &s.list.Users[i]
		if q != "" && !strings.Contains(strings.ToLower(u.ID+" "+u.DisplayName), q) || !s.keep(u) {
			continue
		}
		if u.Person() {
			people = append(people, u)
		} else {
			bridges[u.Bridge] = append(bridges[u.Bridge], u)
		}
	}
	s.sortUsers(people)
	var rows []userRow
	for _, u := range people {
		rows = append(rows, userRow{u: u})
	}
	names := make([]string, 0, len(bridges))
	for b := range bridges {
		names = append(names, b)
	}
	sort.Strings(names)
	for _, b := range names {
		rows = append(rows, userRow{bridge: b, n: len(bridges[b])})
		if s.open[b] || q != "" {
			s.sortUsers(bridges[b])
			for _, u := range bridges[b] {
				rows = append(rows, userRow{u: u, bridge: b})
			}
		}
	}
	return rows
}

// keep reports whether u passes the filter.
func (s *usersScreen) keep(u *deploy.MatrixUser) bool {
	recent := !u.LastSeen.IsZero() && time.Since(u.LastSeen) < activeFor
	switch userFilters[s.filter] {
	case "admins":
		return u.Admin
	case "active":
		return !u.Deactivated && recent
	case "inactive":
		return !u.Deactivated && !recent
	case "deactivated":
		return u.Deactivated
	}
	return true
}

func (s *usersScreen) sortUsers(us []*deploy.MatrixUser) {
	sort.SliceStable(us, func(i, j int) bool {
		a, b := us[i], us[j]
		switch userSorts[s.sortBy] {
		case "name":
			return strings.ToLower(userName(a)) < strings.ToLower(userName(b))
		case "joined":
			return a.Created.After(b.Created)
		case "media":
			return a.MediaBytes > b.MediaBytes
		}
		if a.Deactivated != b.Deactivated { // last seen: the deactivated go last
			return b.Deactivated
		}
		return a.LastSeen.After(b.LastSeen)
	})
}

func (s *usersScreen) current() (userRow, bool) {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return userRow{}, false
	}
	return rows[s.cursor], true
}

func (s *usersScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case usersMsg:
		if m.server != s.server {
			return nil // from before switching servers
		}
		s.busy = false
		s.saveToken(m.token)
		if _, bad := m.err.(deploy.ErrAdminLogin); bad {
			s.err = m.err
			s.loginForm()
			return nil
		}
		s.err = m.err
		if m.err == nil {
			saveCache("userlist", *m.list)
			s.list = m.list
			s.cursor = min(s.cursor, max(0, len(s.rows())-1))
		}
		return nil
	case userOpMsg:
		s.busy = false
		s.saveToken(m.token)
		if _, bad := m.err.(deploy.ErrAdminLogin); bad {
			s.err = m.err
			s.loginForm()
			return nil
		}
		if m.err != nil {
			s.err = m.err
			return nil
		}
		s.info, s.password = "✓ "+m.info, m.password
		return s.reload()
	case spinner.TickMsg:
		return spinning(s.busy, &s.spin, m)
	case formDoneMsg:
		switch s.mode {
		case usersLogin:
			s.login.User, s.login.Password, s.login.Token = s.form.value("user"), s.form.value("pass"), ""
			if s.cfg.AdminUser == "" && s.login.User != "" {
				s.cfg.AdminUser = s.login.User // offered next time
				_ = s.cfg.Save()
			}
			s.mode = usersList
			return s.reload()
		case usersNew:
			return s.create()
		}
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if s.searching {
			var cmd tea.Cmd
			s.search, cmd = s.search.Update(msg)
			return cmd
		}
		return nil
	}
	if k.String() == "esc" && s.mode != usersList {
		s.mode, s.op = usersList, nil
		return nil
	}
	rows := s.rows()
	switch s.mode {
	case usersLogin, usersNew:
		return s.form.Update(msg)
	case usersConfirm:
		switch {
		case (k.String() == "y" || k.String() == "enter") && s.op != nil:
			op := s.op
			s.mode, s.op = usersList, nil
			return s.run(op)
		case k.String() == "n":
			s.mode, s.op = usersList, nil
		case cursorKey(k.String(), s.cursor, len(rows)) != s.cursor: // moving away cancels
			s.mode, s.op = usersList, nil
			s.cursor = cursorKey(k.String(), s.cursor, len(rows))
		}
		return nil
	}
	if s.searching {
		switch k.String() {
		case "enter", "esc", "up", "down":
			s.searching = false
			s.search.Blur()
			if k.String() == "esc" {
				s.search.SetValue("")
			}
			s.cursor = 0
			return nil
		}
		var cmd tea.Cmd
		s.search, cmd = s.search.Update(msg)
		s.cursor = 0
		return cmd
	}
	s.cursor = cursorKey(k.String(), s.cursor, len(rows))
	var r userRow
	if s.cursor < len(rows) {
		r = rows[s.cursor]
	}
	switch k.String() {
	case "esc":
		s.search.SetValue("")
		s.cursor = 0
		return nil
	case "/":
		s.searching = true
		return s.search.Focus()
	case "f":
		s.filter = (s.filter + 1) % len(userFilters)
		s.cursor = 0
		return nil
	case "o":
		s.sortBy = (s.sortBy + 1) % len(userSorts)
		s.cursor = 0
		return nil
	case "enter", " ":
		if r.u == nil && r.bridge != "" {
			s.open[r.bridge] = !s.open[r.bridge]
		}
		return nil
	}
	if s.busy {
		return nil
	}
	var u deploy.MatrixUser
	if r.u != nil && r.u.Person() {
		u = *r.u // a bridge's accounts are the bridge's to change
	}
	ask := func(op *userOp) tea.Cmd {
		s.op, s.mode = op, usersConfirm
		return nil
	}
	switch k.String() {
	case "r":
		return s.reload()
	case "n":
		name := textField("name", "Username", "", "alice", "Just the name, not @alice:"+s.cfg.Domain+".")
		name.required = true
		pw := secretField("pass", "Password", config.RandomPassword(16), "Generated for you; shown once the account exists.")
		pw.required = true
		s.form, s.mode = newForm(name, pw, toggleField("admin", "Make this user a server administrator", false, "")), usersNew
	case "p":
		if u.ID != "" {
			pw := config.RandomPassword(16)
			return ask(&userOp{desc: "new password set for " + u.ID + "; their devices were signed out", user: u.ID,
				body: map[string]any{"password": pw, "logout_devices": true}, password: pw})
		}
	case "s":
		if u.ID != "" && !u.Deactivated {
			return ask(&userOp{desc: u.ID + " signed out everywhere", user: u.ID, signOut: true})
		}
	case "a":
		if u.ID != "" && !u.Deactivated {
			verb := "Make " + u.ID + " a server administrator"
			if u.Admin {
				verb = "Remove " + u.ID + "'s administrator rights"
			}
			return ask(&userOp{desc: strings.ToLower(verb[:1]) + verb[1:] + ": done", user: u.ID, body: map[string]any{"admin": !u.Admin}})
		}
	case "d":
		if u.ID == "" {
			break
		}
		if u.Deactivated && deploy.UsesMAS(s.cfg) {
			return ask(&userOp{desc: u.ID + " reactivated with their old password", user: u.ID, body: map[string]any{"deactivated": false}})
		}
		if u.Deactivated {
			pw := config.RandomPassword(16)
			return ask(&userOp{desc: u.ID + " reactivated", user: u.ID, body: map[string]any{"deactivated": false, "password": pw}, password: pw})
		}
		return ask(&userOp{desc: u.ID + " deactivated", user: u.ID, body: map[string]any{"deactivated": true}})
	}
	return nil
}

func (s *usersScreen) create() tea.Cmd {
	name := strings.ToLower(strings.TrimPrefix(strings.SplitN(s.form.value("name"), ":", 2)[0], "@"))
	pw := s.form.value("pass")
	if name == "" || pw == "" {
		return nil
	}
	id := "@" + name + ":" + s.cfg.Domain
	if s.list != nil {
		for _, u := range s.list.Users {
			if u.ID == id {
				s.err, s.mode = fmt.Errorf("%s already exists", id), usersList
				return nil
			}
		}
	}
	s.mode = usersList
	return s.run(&userOp{desc: id + " created", user: id,
		body: map[string]any{"password": pw, "admin": s.form.get("admin").on}, password: pw})
}

func (s *usersScreen) view(w, h int) string {
	switch s.mode {
	case usersLogin:
		head := sWarn.Render("Signing in as "+s.login.User+" didn't work") + "\n" + sMuted.Render(errString(s.err)) + "\n\n"
		if s.login.User == "" {
			head = sSub.Render("Which account should interzone act as?") + "\n\n"
		}
		return head + s.form.View(w, h-4)
	case usersNew:
		return s.form.View(w, h)
	}
	foot := s.footer(w)
	if s.list == nil {
		return foot
	}
	top := s.stats(w) + "\n" + s.toolbar(w)
	gap := "\n\n"
	if tight {
		gap = "\n"
	}
	listH := max(3, h-lipgloss.Height(top)-lipgloss.Height(foot)-2*len(gap)+2)
	if foot == "" {
		listH += len(gap) - 1
	}
	rows := s.rows()
	s.cursor = min(s.cursor, max(0, len(rows)-1))
	var body string
	switch {
	case len(rows) == 0:
		body = sMuted.Render("Nobody matches.")
	case w >= 84 && !s.narrow: // the list, and the card beside it
		lw := min(56, w*9/20)
		card := ""
		if r, ok := s.current(); ok {
			card = s.card(r, w-lw-2)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(lw+2).Render(s.listView(rows, lw, listH, false)), card)
	default:
		body = s.listView(rows, w, listH, true)
	}
	out := top + gap + body
	if foot != "" {
		out += gap + foot
	}
	return out
}

// footer is the result of the last change, a password to pass on, or the
// question before a change.
func (s *usersScreen) footer(w int) string {
	var status []string
	switch {
	case s.busy:
		status = append(status, s.spin.View()+sMuted.Render(" talking to Synapse on the server…"))
	case s.err != nil:
		status = append(status, sBad.Render("✗ "+s.err.Error()))
	case s.info != "":
		status = append(status, sGood.Render(s.info))
	}
	if s.password != "" {
		status = append(status, sMuted.Render("Password  ")+sTitle.Render(s.password)+sMuted.Render("   (shown once; it isn't saved)"))
	}
	if s.mode != usersConfirm || s.op == nil {
		return strings.Join(status, "\n")
	}
	yes := func(verb string) string {
		return mark("key:enter", keyChip("enter")+sMuted.Render(" "+verb)) + "   " + mark("key:esc", keyChip("esc")+sMuted.Render(" cancel"))
	}
	switch {
	case s.op.body["deactivated"] == true:
		return confirmView(w, sWarn.Render("Deactivate "+s.op.user+"?"),
			sSub.Render("They are signed out everywhere and can't sign in again until reactivated."), "", yes("deactivate"))
	case s.op.signOut:
		return confirmView(w, sWarn.Render("Sign "+s.op.user+" out everywhere?"),
			sSub.Render("Every phone, browser and app they use is signed out; their password stays the same."), "", yes("sign out"))
	}
	return confirmView(w, sWarn.Render(strings.ToUpper(s.op.desc[:1])+s.op.desc[1:]+"?"), "", yes("yes"))
}

// stats is the strip of totals: tiles on a wide terminal, a line on a
// phone.
func (s *usersScreen) stats(w int) string {
	var people, active, admins, fresh, bridged int
	var media int64
	for _, u := range s.list.Users {
		media += u.MediaBytes
		if !u.Person() {
			bridged++
			continue
		}
		if u.Deactivated {
			continue
		}
		people++
		if u.Admin {
			admins++
		}
		if !u.LastSeen.IsZero() && time.Since(u.LastSeen) < activeFor {
			active++
		}
		if time.Since(u.Created) < activeFor {
			fresh++
		}
	}
	type stat struct{ value, label, short string }
	stats := []stat{{fmt.Sprint(people), noun(people, "person", "people"), noun(people, "person", "people")},
		{fmt.Sprint(active), "active (30 days)", "active"},
		{fmt.Sprint(admins), noun(admins, "admin", "admins"), noun(admins, "admin", "admins")},
		{fmt.Sprint(fresh), "new (30 days)", "new"}}
	if s.list.Rooms >= 0 {
		stats = append(stats, stat{fmt.Sprint(s.list.Rooms), noun(s.list.Rooms, "room", "rooms"), noun(s.list.Rooms, "room", "rooms")})
	}
	stats = append(stats, stat{humanBytes(media), "media", "media"})
	if bridged > 0 {
		stats = append(stats, stat{fmt.Sprint(bridged), "bridged", "bridged"})
	}
	if w >= 84 && !s.narrow && !tight {
		var tiles []func(w, h int) string
		for i, st := range stats {
			value := sTitle.Render(st.value)
			if i == 1 && active > 0 {
				value = sGood.Render(st.value)
			}
			tiles = append(tiles, func(w, h int) string {
				label := st.label
				if lipgloss.Width(label) > w-4 {
					label = st.short
				}
				return card("", []string{value, sMuted.Render(label)}, w, h, cFaint)
			})
		}
		return columns(w, tiles...)
	}
	var parts []string
	for _, st := range stats {
		parts = append(parts, sTitle.Render(st.value)+" "+sMuted.Render(st.short))
	}
	lines := flow(parts, w)
	if tight {
		return lines[0]
	}
	return strings.Join(lines, "\n")
}

// toolbar is the search box and what f and o switch.
func (s *usersScreen) toolbar(w int) string {
	search := mark("key:/", keyChip("/")+sMuted.Render(" search"))
	if s.searching || s.search.Value() != "" {
		s.search.Width = max(8, min(30, w/3))
		search = s.search.View()
	}
	filter := mark("key:f", keyChip("f")+sMuted.Render(" ")+sAccent.Render(userFilters[s.filter]))
	order := mark("key:o", keyChip("o")+sMuted.Render(" by ")+sAccent.Render(userSorts[s.sortBy]))
	return truncateANSI(search+"   "+filter+"   "+order, w)
}

// listView draws rows w wide in h lines. inline puts the card of the row
// under the cursor in the list itself, as on a phone.
func (s *usersScreen) listView(rows []userRow, w, h int, inline bool) string {
	var lines []string
	cur := 0
	for i, r := range rows {
		id := "row:" + fmt.Sprint(i)
		if i == s.cursor {
			cur = len(lines)
			if inline && r.u != nil && !tight {
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

// rowLine is one row of the list, w wide.
func (s *usersScreen) rowLine(r userRow, w int, on bool) string {
	if r.u == nil {
		arrow := "▸"
		if s.open[r.bridge] || s.search.Value() != "" {
			arrow = "▾"
		}
		left := sPink.Render("⇄") + "  " + sTitle.Render(bridgeName(r.bridge)) + sMuted.Render(fmt.Sprintf("  %d %s", r.n, noun(r.n, "account", "accounts")))
		return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-2)) + sFaint.Render(arrow)
	}
	u := r.u
	indent := ""
	if !u.Person() {
		indent = "   "
	}
	nameStyle := lipgloss.NewStyle().Foreground(cText)
	if on {
		nameStyle = sTitle
	}
	if u.Deactivated {
		nameStyle = sFaint
	}
	right := sMuted.Render(ago(u.LastSeen))
	if u.Deactivated {
		right = sFaint.Render("deactivated")
	}
	var tags string
	if u.Admin {
		tags += " " + tagChip("admin", cPink)
	}
	if u.Bot {
		tags += " " + tagChip("bot", cMuted)
	}
	room := w - lipgloss.Width(indent) - 3 - lipgloss.Width(tags) - lipgloss.Width(right) - 2
	left := indent + activity(*u) + "  " + nameStyle.Render(truncate(userName(u), max(6, room))) + tags
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

// card is everything about the row under the cursor, w wide.
func (s *usersScreen) card(r userRow, w int) string {
	inner := w - 4
	if r.u == nil {
		return s.bridgeCard(r, w)
	}
	u := r.u
	name := gradient(truncate(userName(u), inner-4))
	if u.Deactivated {
		name = sFaint.Render(truncate(userName(u), inner-4))
	}
	var tags []string
	if u.Admin {
		tags = append(tags, tagChip("admin", cPink))
	}
	if u.Deactivated {
		tags = append(tags, tagChip("deactivated", cRed))
	}
	if u.Locked {
		tags = append(tags, tagChip("locked", cAmber))
	}
	if u.ShadowBanned {
		tags = append(tags, tagChip("shadow-banned", cAmber))
	}
	if u.Bot {
		tags = append(tags, tagChip("bot", cMuted))
	}
	lines := []string{activity(*u) + "  " + name}
	if t := strings.Join(tags, " "); t != "" {
		if lipgloss.Width(lines[0])+2+lipgloss.Width(t) <= inner {
			lines[0] += "  " + t
		} else {
			lines = append(lines, "   "+t)
		}
	}
	lines = append(lines, "   "+sMuted.Render(truncate(u.ID, inner-3)), "")
	kv := func(k, v string) {
		lines = append(lines, sMuted.Render(fmt.Sprintf("%-11s", k))+v)
	}
	seen := sFaint.Render("never")
	if !u.LastSeen.IsZero() {
		seen = sTitle.Render(ago(u.LastSeen)) + sFaint.Render("  "+u.LastSeen.Local().Format("2006-01-02 15:04"))
	}
	kv("Last seen", seen)
	if !u.Created.IsZero() {
		kv("Joined", sTitle.Render(u.Created.Local().Format("2006-01-02"))+sFaint.Render("  "+ago(u.Created)))
	}
	if u.Devices >= 0 {
		kv("Sessions", sTitle.Render(fmt.Sprint(u.Devices))+sMuted.Render(" "+noun(u.Devices, "device", "devices")))
	}
	if u.Rooms >= 0 {
		kv("Rooms", sTitle.Render(fmt.Sprint(u.Rooms)))
	}
	media := sFaint.Render("none")
	if u.MediaCount > 0 {
		media = sTitle.Render(humanBytes(u.MediaBytes)) + sMuted.Render(fmt.Sprintf("  %d %s", u.MediaCount, noun(u.MediaCount, "file", "files")))
	}
	kv("Media", media)
	if len(u.Emails) > 0 {
		kv("Email", sTitle.Render(truncate(strings.Join(u.Emails, ", "), inner-11)))
	}
	if u.Type != "" {
		kv("Type", sTitle.Render(u.Type))
	}
	if !u.Person() {
		kv("Bridge", sTitle.Render(bridgeName(u.Bridge)))
	}
	kv("Picture", map[bool]string{true: sTitle.Render("set"), false: sFaint.Render("none")}[u.Avatar])

	if u.Person() {
		lines = append(lines, "")
		var acts []string
		act := func(k, label string) { acts = append(acts, mark("key:"+k, keyChip(k)+" "+sMuted.Render(label))) }
		if u.Deactivated {
			act("d", "reactivate")
		} else {
			act("p", "new password")
			act("a", map[bool]string{true: "remove admin", false: "make admin"}[u.Admin])
			act("s", "sign out everywhere")
			act("d", "deactivate")
		}
		lines = append(lines, flow(acts, inner)...)
	} else {
		lines = append(lines, "", sFaint.Render("Managed by the "+bridgeName(u.Bridge)+" bridge."))
	}
	return picked(lines, w)
}

// bridgeCard sums up a bridge's accounts.
func (s *usersScreen) bridgeCard(r userRow, w int) string {
	var bot string
	var active, total int
	var media int64
	for _, u := range s.list.Users {
		if u.Bridge != r.bridge {
			continue
		}
		total++
		media += u.MediaBytes
		if u.Bot {
			bot = u.ID
		}
		if !u.LastSeen.IsZero() && time.Since(u.LastSeen) < activeFor {
			active++
		}
	}
	kv := func(k, v string) string { return sMuted.Render(fmt.Sprintf("%-11s", k)) + v }
	lines := []string{sPink.Render("⇄") + "  " + gradient(bridgeName(r.bridge)) + sMuted.Render("  bridge"), "",
		kv("Accounts", sTitle.Render(fmt.Sprint(total))+sMuted.Render(fmt.Sprintf("  %d active this month", active)))}
	if bot != "" {
		lines = append(lines, kv("Bot", sTitle.Render(truncate(bot, w-15))))
	}
	lines = append(lines, kv("Media", sTitle.Render(humanBytes(media))), "",
		sFaint.Render("Puppets stand in for people on the other network;"), sFaint.Render("the bridge creates and manages them."))
	return picked(lines, w)
}

// flow lays chips out in lines of w columns.
func flow(chips []string, w int) []string {
	var lines []string
	line := ""
	for _, c := range chips {
		switch {
		case line == "":
			line = c
		case lipgloss.Width(line)+3+lipgloss.Width(c) <= w:
			line += "   " + c
		default:
			lines = append(lines, line)
			line = c
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// activity is a dot for how recently someone was around.
func activity(u deploy.MatrixUser) string {
	switch {
	case u.Deactivated:
		return sFaint.Render("⊘")
	case u.LastSeen.IsZero():
		return sFaint.Render("○")
	case time.Since(u.LastSeen) < 7*24*time.Hour:
		return sGood.Render("●")
	case time.Since(u.LastSeen) < activeFor:
		return sWarn.Render("●")
	}
	return sFaint.Render("●")
}

// userName is the display name, or the name in the ID without one.
func userName(u *deploy.MatrixUser) string {
	if strings.TrimSpace(u.DisplayName) != "" {
		return u.DisplayName
	}
	return strings.SplitN(strings.TrimPrefix(u.ID, "@"), ":", 2)[0]
}

// bridgeName turns an appservice ID such as "mautrix-telegram" into
// "Telegram".
func bridgeName(id string) string {
	n := id
	for _, p := range []string{"mautrix-", "matrix-", "mx-puppet-", "appservice-"} {
		n = strings.TrimPrefix(n, p)
	}
	n = strings.TrimSuffix(strings.TrimSuffix(n, "-bridge"), "bridge")
	n = strings.Trim(strings.ReplaceAll(n, "_", " "), " -")
	if n == "" {
		return id
	}
	return strings.ToUpper(n[:1]) + n[1:]
}

// humanBytes is a size in the largest unit that keeps it above 1.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, units := float64(n)/unit, "KMGTP"
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, units[i])
	}
	return fmt.Sprintf("%.1f %cB", v, units[i])
}

// noun is one or many, for n.
func noun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
