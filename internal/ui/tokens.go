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

// ── Tokens ─────────────────────────────────────────────────────────
//
// The registration tokens people sign up to MAS with: how often each was
// used, until when it works, and the token interzone made for sign-up.
// New ones can be made, limited in uses or time, and any can be revoked
// and brought back.

type tokensMsg struct {
	server string
	gen    int
	list   []deploy.RegToken
	err    error
}

func (tokensMsg) owner() string { return "Tokens" }

type tokenOpMsg struct {
	info string
	err  error
}

func (tokenOpMsg) owner() string { return "Tokens" }

type tokensScreen struct {
	cfg     *config.Config
	server  string
	list    []deploy.RegToken
	loaded  bool
	cursor  int
	form    *form // a new token
	confirm bool  // revoking or restoring the one under the cursor
	narrow  bool

	busy bool
	gen  int
	err  error
	info string
	spin spinner.Model
}

func newTokensScreen() *tokensScreen { return &tokensScreen{spin: newSpinner()} }

func (s *tokensScreen) tool()                {}
func (s *tokensScreen) name() string         { return "Tokens" }
func (s *tokensScreen) title() string        { return "Sign-up tokens on " + s.cfg.Domain }
func (s *tokensScreen) typing() bool         { return s.form != nil && s.form.typing() }
func (s *tokensScreen) handlesEsc() bool     { return s.form != nil || s.confirm }
func (s *tokensScreen) leave(*config.Config) {}
func (s *tokensScreen) setCompact(on bool)   { s.narrow = on }
func (s *tokensScreen) activeForm() *form    { return s.form }

func (s *tokensScreen) keys() [][2]string {
	switch {
	case s.form != nil:
		return [][2]string{{"tab/↑↓", "move"}, {"enter", "create"}, {"esc", "cancel"}}
	case s.confirm:
		return [][2]string{{"enter", "do it"}, {"esc", "cancel"}}
	}
	keys := [][2]string{{"↑↓", "select"}, {"n", "new"}}
	if t, ok := s.current(); ok {
		keys = append(keys, [2]string{"x", map[bool]string{true: "restore", false: "revoke"}[t.RevokedAt != nil]})
	}
	return append(keys, [2]string{"r", "reload"})
}

func (s *tokensScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.form, s.confirm, s.info = nil, false, ""
	if srv := config.Active(); srv != s.server {
		s.server, s.list, s.loaded, s.cursor = srv, nil, false, 0
	}
	if !deploy.UsesMAS(c) {
		return nil
	}
	return s.reload()
}

func (s *tokensScreen) reload() tea.Cmd {
	s.busy, s.err = true, nil
	s.gen++
	cfg, srv, gen := *s.cfg.Clone(), s.server, s.gen
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		list, err := deploy.ListTokens(ctx, &cfg)
		return tokensMsg{srv, gen, list, err}
	})
}

// do runs a change, then reloads the list.
func (s *tokensScreen) do(info string, fn func(ctx context.Context, c *config.Config) error) tea.Cmd {
	s.busy, s.err, s.info = true, nil, ""
	cfg := *s.cfg.Clone()
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return tokenOpMsg{info, fn(ctx, &cfg)}
	})
}

func (s *tokensScreen) current() (deploy.RegToken, bool) {
	if s.cursor < 0 || s.cursor >= len(s.list) {
		return deploy.RegToken{}, false
	}
	return s.list[s.cursor], true
}

func (s *tokensScreen) newForm() {
	tok := textField("token", "Token", config.RandomPassword(12), "", "What people type to sign up. Generated for you; change it if you like.")
	tok.required = true
	uses := textField("uses", "Uses", "", "unlimited", "How many accounts it can make. Empty for no limit.")
	days := textField("days", "Days", "", "never expires", "How many days it works for. Empty for no end.")
	s.form = newForm(tok, uses, days)
}

// create makes the token the form describes.
func (s *tokensScreen) create() tea.Cmd {
	tok := strings.TrimSpace(s.form.value("token"))
	num := func(key, what string) (int, bool) {
		v := strings.TrimSpace(s.form.value(key))
		if v == "" {
			return 0, true
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			s.err = fmt.Errorf("%s should be a whole number above 0, or empty", what)
			return 0, false
		}
		return n, true
	}
	uses, ok1 := num("uses", "uses")
	days, ok2 := num("days", "days")
	if tok == "" || !ok1 || !ok2 {
		return nil
	}
	s.form = nil
	return s.do("token "+tok+" made", func(ctx context.Context, c *config.Config) error {
		return deploy.CreateToken(ctx, c, tok, uses, time.Duration(days)*24*time.Hour)
	})
}

func (s *tokensScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tokensMsg:
		if m.server != s.server || m.gen != s.gen {
			return nil
		}
		s.busy, s.err = false, m.err
		if m.err == nil {
			s.list, s.loaded = m.list, true
			s.cursor = min(s.cursor, max(0, len(s.list)-1))
		}
		return nil
	case tokenOpMsg:
		s.busy = false
		if m.err != nil {
			s.err = m.err
			return nil
		}
		s.info = "✓ " + m.info
		return s.reload()
	case spinner.TickMsg:
		return spinning(s.busy, &s.spin, m)
	case formDoneMsg:
		if s.form != nil {
			return s.create()
		}
		return nil
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
		t, ok := s.current()
		switch k.String() {
		case "enter", "y":
			s.confirm = false
			if ok {
				revoke := t.RevokedAt == nil
				info := map[bool]string{true: "token " + t.Token + " revoked", false: "token " + t.Token + " works again"}[revoke]
				return s.do(info, func(ctx context.Context, c *config.Config) error {
					return deploy.RevokeToken(ctx, c, t.Token, revoke)
				})
			}
		case "esc", "n":
			s.confirm = false
		default:
			if next := cursorKey(k.String(), s.cursor, len(s.list)); next != s.cursor { // moving away cancels
				s.confirm, s.cursor = false, next
			}
		}
		return nil
	}
	s.cursor = cursorKey(k.String(), s.cursor, len(s.list))
	if s.busy || !deploy.UsesMAS(s.cfg) {
		return nil
	}
	switch k.String() {
	case "r":
		return s.reload()
	case "n":
		s.err, s.info = nil, ""
		s.newForm()
	case "x":
		if _, ok := s.current(); ok {
			s.confirm = true
		}
	}
	return nil
}

func (s *tokensScreen) click(id string) (tea.Cmd, bool) {
	if s.form != nil || s.confirm || s.busy {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.list), s.update)
}

func (s *tokensScreen) view(w, h int) string {
	if !deploy.UsesMAS(s.cfg) {
		return sMuted.Width(min(w, 70)).Render("Sign-up tokens belong to the Matrix Authentication Service, which this server doesn't use. Synapse with MAS has them; turn MAS on in the setup to use them.")
	}
	if s.form != nil {
		head := sSub.Render("A new sign-up token") + "\n"
		if s.err != nil {
			head += sBad.Render("✗ "+s.err.Error()) + "\n"
		}
		return head + "\n" + s.form.View(w, h-3)
	}
	top := s.status(w)
	foot := s.footer(w)
	if !s.loaded {
		return top + "\n\n" + foot
	}
	listH := max(3, h-lipgloss.Height(top)-lipgloss.Height(foot)-4)
	var body string
	switch {
	case len(s.list) == 0:
		body = sMuted.Render("No tokens yet. ") + mark("key:n", keyChip("n")+sMuted.Render(" makes one."))
	case w >= 84 && !s.narrow:
		lw := min(56, w*9/20)
		card := ""
		if t, ok := s.current(); ok {
			card = s.card(t, w-lw-2)
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

// status says whether tokens do anything right now.
func (s *tokensScreen) status(w int) string {
	line := sWarn.Render("○ Sign-up is closed") + sMuted.Render(", so tokens don't work until it's opened on Manage.")
	if s.cfg.RegistrationOpen() {
		line = sGood.Render("● Sign-up is open") + sMuted.Render(" to anyone with an active token.")
	}
	return lipgloss.NewStyle().Width(w).Render(line)
}

func (s *tokensScreen) footer(w int) string {
	var status string
	switch {
	case s.busy:
		status = s.spin.View() + sMuted.Render(" talking to MAS on the server…")
	case s.err != nil:
		status = sBad.Render("✗ " + s.err.Error())
	case s.info != "":
		status = sGood.Render(s.info)
	}
	t, ok := s.current()
	if !s.confirm || !ok {
		return status
	}
	if t.RevokedAt != nil {
		return confirmView(w, sWarn.Render("Let "+t.Token+" work again?"), "", confirmKeys("enter", "restore"))
	}
	return confirmView(w, sWarn.Render("Revoke "+t.Token+"?"),
		sSub.Render("Nobody can sign up with it any more; accounts made with it stay."), "", confirmKeys("enter", "revoke"))
}

func (s *tokensScreen) listView(w, h int, inline bool) string {
	var lines []string
	cur := 0
	for i, t := range s.list {
		id := "row:" + fmt.Sprint(i)
		if i == s.cursor {
			cur = len(lines)
			if inline && !tight {
				lines = append(lines, markLines(id, s.card(t, min(w, maxFormWidth))))
				continue
			}
			lines = append(lines, markRow(id, sAccent.Render("▌")+" "+s.rowLine(t, w-2, true), w))
			continue
		}
		lines = append(lines, markRow(id, "  "+s.rowLine(t, w-2, false), w))
	}
	return windowAround(strings.Join(lines, "\n"), cur, h)
}

// stateDot is a dot for whether a token works.
func stateDot(state string) string {
	if state == "active" {
		return sGood.Render("●")
	}
	return sFaint.Render("⊘")
}

func (s *tokensScreen) rowLine(t deploy.RegToken, w int, on bool) string {
	state := t.State(time.Now())
	name := lipgloss.NewStyle().Foreground(cText)
	if on {
		name = sTitle
	}
	if state != "active" {
		name = sFaint
	}
	right := sMuted.Render(tokenUses(t))
	if state != "active" {
		right = sFaint.Render(state)
	}
	var tags string
	if t.Token == s.cfg.SignupToken {
		tags = " " + tagChip("interzone", cPink)
	}
	room := w - 3 - lipgloss.Width(tags) - lipgloss.Width(right) - 2
	left := stateDot(state) + "  " + name.Render(truncate(t.Token, max(6, room))) + tags
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

// tokenUses is how much of a token has been used.
func tokenUses(t deploy.RegToken) string {
	if t.UsageLimit == nil {
		return fmt.Sprintf("%d used", t.TimesUsed)
	}
	return fmt.Sprintf("%d/%d used", t.TimesUsed, *t.UsageLimit)
}

func (s *tokensScreen) card(t deploy.RegToken, w int) string {
	inner := w - 4
	state := t.State(time.Now())
	head := stateDot(state) + "  " + gradient(truncate(t.Token, inner-4))
	if state != "active" {
		head = stateDot(state) + "  " + sFaint.Render(truncate(t.Token, inner-4)) + "  " + tagChip(state, cAmber)
	}
	lines := []string{head}
	if t.Token == s.cfg.SignupToken {
		lines = append(lines, "   "+sMuted.Render("the token interzone made for sign-up"))
	}
	lines = append(lines, "")
	kv := func(k, v string) { lines = append(lines, sMuted.Render(fmt.Sprintf("%-11s", k))+v) }
	limit := sFaint.Render("  no limit")
	if t.UsageLimit != nil {
		limit = sMuted.Render(fmt.Sprintf("  of %d", *t.UsageLimit))
	}
	kv("Used", sTitle.Render(fmt.Sprint(t.TimesUsed))+limit)
	last := sFaint.Render("never")
	if t.LastUsedAt != nil {
		last = sTitle.Render(ago(*t.LastUsedAt))
	}
	kv("Last used", last)
	kv("Made", sTitle.Render(t.CreatedAt.Local().Format("2006-01-02"))+sFaint.Render("  "+ago(t.CreatedAt)))
	expires := sFaint.Render("never")
	if t.ExpiresAt != nil {
		expires = sTitle.Render(t.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	kv("Expires", expires)
	if t.RevokedAt != nil {
		kv("Revoked", sTitle.Render(t.RevokedAt.Local().Format("2006-01-02 15:04")))
	}
	lines = append(lines, "")
	act := map[bool]string{true: "restore", false: "revoke"}[t.RevokedAt != nil]
	lines = append(lines, mark("key:x", keyChip("x")+" "+sMuted.Render(act))+"   "+mark("key:n", keyChip("n")+" "+sMuted.Render("new token")))
	return picked(lines, w)
}
