package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"interzone/internal/catalog"
	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Tuning ─────────────────────────────────────────────────────────
//
// Every variable of every playbook role (the playbook's own and the ones
// installed from requirements.yml), with its default and the comment the
// role documents it with. Changed values are saved in Config.Overrides and
// written into vars.yml.

type defaultsMsg struct {
	err error
}

// tuneRole is a role as the screen lists it.
type tuneRole struct {
	config.Role
	label string // the component it belongs to, if interzone knows it
	inUse bool   // the homeserver, or a component enabled in the setup
}

type tuningScreen struct {
	cfg   *config.Config
	roles []tuneRole
	err   error

	role       int // index into roles of the open role, or -1 for the list
	roleCursor int

	fetching bool
	spin     spinner.Model

	filter      textinput.Model
	filtering   bool
	onlyChanged bool
	cursor      int

	editing bool
	editor  textarea.Model
	editErr string
}

func newTuningScreen() *tuningScreen {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	f := textinput.New()
	f.Prompt = ""
	f.Placeholder = "type to filter by name or description"
	f.PlaceholderStyle = lipgloss.NewStyle().Foreground(cFaint)
	f.TextStyle = lipgloss.NewStyle().Foreground(cText)
	return &tuningScreen{spin: sp, filter: f, role: -1}
}

func (s *tuningScreen) tool()        {}
func (s *tuningScreen) name() string { return "Tuning" }
func (s *tuningScreen) title() string {
	if r, ok := s.openRole(); ok {
		return "Fine-tune " + r.title()
	}
	return "Fine-tune the playbook's roles"
}
func (s *tuningScreen) typing() bool { return s.filtering || s.editing }
func (s *tuningScreen) handlesEsc() bool {
	return s.editing || s.filtering || s.filter.Value() != "" || s.role >= 0
}
func (s *tuningScreen) keys() [][2]string {
	switch {
	case s.editing:
		k := [][2]string{{"tab", "save value"}, {"esc", "cancel"}}
		if s.multiline() {
			k = append([][2]string{{"enter", "new line"}}, k...)
		} else {
			k = append([][2]string{{"enter", "save value"}}, k[1:]...)
		}
		return k
	case s.filtering:
		return [][2]string{{"enter", "done"}, {"esc", "clear"}}
	case s.err != nil:
		return [][2]string{{"f", "download the playbook"}}
	case s.role < 0:
		return [][2]string{{"↑↓", "move"}, {"enter", "open"}, {"/", "search all settings"}, {"c", "changed only"}}
	}
	return [][2]string{{"↑↓", "move"}, {"enter", "change"}, {"del", "reset"}, {"/", "filter"}, {"c", "changed only"}}
}

func (r tuneRole) title() string {
	if r.label != "" {
		return r.label + " (" + r.Name + ")"
	}
	return r.Name
}

func homeserverName(id string) string {
	for _, h := range catalog.Homeservers {
		if h.ID == id {
			return h.Name
		}
	}
	return id
}

func (s *tuningScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.editing, s.editErr = false, ""
	s.load()
	return nil
}

func (s *tuningScreen) load() {
	roles, err := config.Roles(s.cfg.PlaybookDir)
	s.err = err
	open := ""
	if r, ok := s.openRole(); ok {
		open = r.Name
	}
	// What each role belongs to: the component whose enable variable it
	// defines, or the homeserver.
	owner := map[string]catalog.Component{}
	for _, comp := range catalog.Components {
		if comp.EnableVar != "" {
			owner[comp.EnableVar] = comp
		}
	}
	s.roles, s.role = nil, -1
	for _, r := range roles {
		tr := tuneRole{Role: r}
		if r.Name == config.PlaybookRole {
			tr.label, tr.inUse = "Settings the playbook itself defines", true
		}
		if r.Name == config.HomeserverRole[s.cfg.Homeserver] {
			tr.label, tr.inUse = homeserverName(s.cfg.Homeserver), true
		}
		if ev, ok := enableVar(r); ok && tr.label == "" {
			if comp, known := owner[ev.Name]; known {
				tr.label, tr.inUse = comp.Name, s.cfg.Components[comp.ID]
			} else {
				tr.inUse = ev.Effective() == "true" // core services; a computed value is unknown here
			}
		}
		s.roles = append(s.roles, tr)
	}
	sort.SliceStable(s.roles, func(i, j int) bool { return s.roles[i].inUse && !s.roles[j].inUse })
	for i, r := range s.roles {
		if r.Name == open {
			s.role = i
		}
	}
	s.cursor = min(s.cursor, max(0, len(s.items())-1))
	s.roleCursor = min(s.roleCursor, max(0, len(s.roleItems())-1))
}

// enableVar is the variable that turns a role's service on: an
// <prefix>_enabled whose prefix most of the role's variables share, so a
// role that merely defines some other service's flag isn't taken for it.
func enableVar(r config.Role) (config.RoleVar, bool) {
	best, bestN := config.RoleVar{}, 0
	for _, v := range r.Vars {
		prefix, ok := strings.CutSuffix(v.Name, "_enabled")
		if !ok {
			continue
		}
		n := 0
		for _, o := range r.Vars {
			if strings.HasPrefix(o.Name, prefix+"_") {
				n++
			}
		}
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best, bestN*2 >= len(r.Vars)
}

func (s *tuningScreen) leave(*config.Config) { s.editing, s.filtering = false, false }

func (s *tuningScreen) openRole() (tuneRole, bool) {
	if s.role < 0 || s.role >= len(s.roles) {
		return tuneRole{}, false
	}
	return s.roles[s.role], true
}

func (s *tuningScreen) query() string { return strings.ToLower(strings.TrimSpace(s.filter.Value())) }

func (s *tuningScreen) matches(v config.RoleVar, q string) bool {
	if _, changed := s.cfg.Overrides[v.Name]; s.onlyChanged && !changed {
		return false
	}
	return q == "" || strings.Contains(v.Name, q) || strings.Contains(strings.ToLower(v.Help), q)
}

// roleRow is a role in the list with how many of its settings match the
// search and how many are changed.
type roleRow struct {
	idx              int
	matching, change int
	nameHit          bool
}

// roleItems is the filtered role list. A search matches role names and
// the settings inside each role.
func (s *tuningScreen) roleItems() []roleRow {
	q := s.query()
	var out []roleRow
	for i, r := range s.roles {
		row := roleRow{idx: i}
		for _, v := range r.Vars {
			if _, ok := s.cfg.Overrides[v.Name]; ok {
				row.change++
			}
			if q != "" && s.matches(v, q) {
				row.matching++
			}
		}
		nameHit := q != "" && (strings.Contains(r.Name, q) || strings.Contains(strings.ToLower(r.label), q))
		switch {
		case s.onlyChanged && row.change == 0:
			continue
		case q != "" && !nameHit && row.matching == 0:
			continue
		}
		row.nameHit = nameHit
		out = append(out, row)
	}
	// Roles the search names come before roles that merely mention it.
	sort.SliceStable(out, func(i, j int) bool { return out[i].nameHit && !out[j].nameHit })
	return out
}

// items is the open role's filtered settings. A search typed on the role
// list keeps applying inside the role.
func (s *tuningScreen) items() []config.RoleVar {
	r, ok := s.openRole()
	if !ok {
		return nil
	}
	q := s.query()
	var out []config.RoleVar
	for _, v := range r.Vars {
		if s.matches(v, q) {
			out = append(out, v)
		}
	}
	return out
}

func (s *tuningScreen) current() (config.RoleVar, bool) {
	items := s.items()
	if s.cursor < 0 || s.cursor >= len(items) {
		return config.RoleVar{}, false
	}
	return items[s.cursor], true
}

func (s *tuningScreen) value(v config.RoleVar) (string, bool) {
	if o, ok := s.cfg.Overrides[v.Name]; ok {
		return o, true
	}
	return v.Effective(), false
}

func (s *tuningScreen) multiline() bool {
	return strings.Contains(s.editor.Value(), "\n")
}

func (s *tuningScreen) startEdit(v config.RoleVar) {
	val, _ := s.value(v)
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.CharLimit = 0
	ta.SetValue(val)
	ta.SetHeight(max(1, min(10, strings.Count(val, "\n")+1)))
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(cPurple)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(cText)
	ta.Focus()
	s.editor, s.editing, s.editErr = ta, true, ""
}

// saveEdit stores the edited value, or drops the override if it is the
// default again.
func (s *tuningScreen) saveEdit() {
	v, ok := s.current()
	if !ok {
		s.editing = false
		return
	}
	val := strings.TrimRight(s.editor.Value(), "\n ")
	if strings.TrimSpace(val) == "" {
		val = "''"
	}
	if err := yaml.Unmarshal([]byte(val), new(any)); err != nil {
		s.editErr = "not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")
		return
	}
	if val == v.Effective() {
		delete(s.cfg.Overrides, v.Name)
	} else {
		s.cfg.Overrides[v.Name] = val
	}
	_ = s.cfg.Save()
	s.editing = false
}

func (s *tuningScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case defaultsMsg:
		s.fetching = false
		if m.err != nil {
			s.err = m.err
			return nil
		}
		s.load()
		return nil
	case spinner.TickMsg:
		if s.fetching {
			var cmd tea.Cmd
			s.spin, cmd = s.spin.Update(m)
			return cmd
		}
		return nil
	case tea.KeyMsg:
		return s.key(m)
	}
	return nil
}

func (s *tuningScreen) key(k tea.KeyMsg) tea.Cmd {
	if s.editing {
		switch k.String() {
		case "esc":
			s.editing = false
			return nil
		case "tab":
			s.saveEdit()
			return nil
		case "enter":
			if !s.multiline() {
				s.saveEdit()
				return nil
			}
		}
		var cmd tea.Cmd
		s.editor, cmd = s.editor.Update(k)
		if lines := s.editor.LineCount(); lines > s.editor.Height() && lines <= 10 {
			s.editor.SetHeight(lines)
		}
		return cmd
	}
	if s.filtering {
		switch k.String() {
		case "esc":
			s.filter.SetValue("")
			s.filtering = false
			s.filter.Blur()
		case "enter", "down", "up":
			s.filtering = false
			s.filter.Blur()
		default:
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(k)
			s.cursor = 0
			return cmd
		}
		return nil
	}
	if s.err != nil {
		if k.String() == "f" && !s.fetching {
			s.fetching = true
			cfg := *s.cfg
			return tea.Batch(s.spin.Tick, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				return defaultsMsg{err: deploy.FetchPlaybook(ctx, &cfg)}
			})
		}
		return nil
	}
	if s.role < 0 {
		return s.roleKey(k)
	}
	n := len(s.items())
	switch k.String() {
	case "esc":
		if s.filter.Value() != "" {
			s.filter.SetValue("")
			s.cursor = 0
			return nil
		}
		s.role, s.cursor = -1, 0
	case "/":
		s.filtering = true
		s.filter.Focus()
	case "c":
		s.onlyChanged = !s.onlyChanged
		s.cursor = 0
	case "up", "k":
		s.cursor = max(0, s.cursor-1)
	case "down", "j":
		s.cursor = min(n-1, s.cursor+1)
	case "pgup":
		s.cursor = max(0, s.cursor-10)
	case "pgdown":
		s.cursor = max(0, min(n-1, s.cursor+10))
	case "home", "g":
		s.cursor = 0
	case "end", "G":
		s.cursor = max(0, n-1)
	case "enter", " ":
		if v, ok := s.current(); ok {
			s.startEdit(v)
		}
	case "delete", "backspace", "x":
		if v, ok := s.current(); ok {
			delete(s.cfg.Overrides, v.Name)
			_ = s.cfg.Save()
		}
	}
	return nil
}

func (s *tuningScreen) roleKey(k tea.KeyMsg) tea.Cmd {
	rows := s.roleItems()
	switch k.String() {
	case "esc":
		s.filter.SetValue("")
		s.roleCursor = 0
	case "/":
		s.filtering = true
		s.filter.Focus()
	case "c":
		s.onlyChanged = !s.onlyChanged
		s.roleCursor = 0
	case "enter", " ", "right", "l":
		if s.roleCursor < len(rows) {
			if rows[s.roleCursor].nameHit {
				s.filter.SetValue("") // the search found the role, not settings in it
			}
			s.role, s.cursor = rows[s.roleCursor].idx, 0
		}
	case "pgup":
		s.roleCursor = max(0, s.roleCursor-10)
	case "pgdown":
		s.roleCursor = max(0, min(len(rows)-1, s.roleCursor+10))
	default:
		s.roleCursor = cursorKey(k.String(), s.roleCursor, len(rows))
	}
	return nil
}

func (s *tuningScreen) view(w, h int) string {
	if s.fetching {
		return s.spin.View() + sMuted.Render(" downloading the playbook…")
	}
	if s.err != nil {
		return sWarn.Render("The playbook isn't downloaded yet, so its settings can't be listed.") + "\n\n" +
			sMuted.Render("Press ") + sKey.Render("f") + sMuted.Render(" to download it from "+s.cfg.Sources.PlaybookGit+".") + "\n\n" +
			sFaint.Render(s.err.Error())
	}
	w = min(w, 120)
	if s.role < 0 {
		return s.rolesView(w, h)
	}
	items := s.items()
	r := s.roles[s.role]
	changed := 0
	for _, v := range r.Vars {
		if _, ok := s.cfg.Overrides[v.Name]; ok {
			changed++
		}
	}
	head := sMuted.Render(fmt.Sprintf("%d settings in the %s role", len(r.Vars), r.Name))
	if changed > 0 {
		head += sFaint.Render("  ·  ") + sPink.Render(fmt.Sprintf("%d changed", changed))
	}
	if s.onlyChanged {
		head += sFaint.Render("  ·  showing changed only")
	}
	filter := sFaint.Render("/ ") + s.filter.View()
	if !s.filtering && s.filter.Value() == "" {
		filter = sFaint.Render("/ to filter")
	}

	detail := s.detailView(w)
	listH := max(3, h-lipgloss.Height(detail)-5)
	nameW := min(54, w/2)
	start := max(0, min(s.cursor-listH/2, len(items)-listH))
	var rows []string
	for i := start; i < len(items) && i < start+listH; i++ {
		v := items[i]
		val, over := s.value(v)
		first, _, more := strings.Cut(val, "\n")
		if more {
			first += " …"
		}
		bar, nameStyle := "   ", sMuted
		if i == s.cursor {
			bar, nameStyle = sAccent.Render("▌")+"  ", sTitle
		}
		mark := " "
		valStyle := sFaint
		if over {
			mark, valStyle = sPink.Render("●"), sPink
		} else if !v.Derived {
			valStyle = sSub
		}
		name := lipgloss.NewStyle().Width(nameW).Render(nameStyle.Render(truncate(v.Name, nameW-2)))
		rows = append(rows, bar+mark+" "+name+valStyle.Render(truncate(first, max(8, w-nameW-6))))
	}
	if len(items) == 0 {
		rows = append(rows, sMuted.Render("   nothing matches"))
	}
	return head + "\n" + filter + "\n\n" + strings.Join(rows, "\n") + "\n\n" + detail
}

func (s *tuningScreen) rolesView(w, h int) string {
	rows := s.roleItems()
	total, galaxy := 0, false
	for _, r := range s.roles {
		total += len(r.Vars)
		galaxy = galaxy || r.Galaxy
	}
	head := sMuted.Render(fmt.Sprintf("%d roles, %d settings", len(s.roles), total))
	if n := len(s.cfg.Overrides); n > 0 {
		head += sFaint.Render("  ·  ") + sPink.Render(fmt.Sprintf("%d changed", n))
	}
	if s.onlyChanged {
		head += sFaint.Render("  ·  showing changed only")
	}
	filter := sFaint.Render("/ ") + s.filter.View()
	if !s.filtering && s.filter.Value() == "" {
		filter = sFaint.Render("/ to search every role's settings")
	}
	foot := ""
	if !galaxy {
		foot = "\n\n" + sFaint.Render("Roles from requirements.yml (Traefik, Postgres, ntfy, Jitsi …) appear after the first install installs them.")
	}
	nameW, labelW := min(40, w/3), min(36, w/3)
	var lines []string
	for i, row := range rows {
		r := s.roles[row.idx]
		bar, nameStyle := "   ", sMuted
		if i == s.roleCursor {
			bar, nameStyle = sAccent.Render("▌")+"  ", sTitle
		}
		mark := " "
		if row.change > 0 {
			mark = sPink.Render("●")
		}
		count := plural(len(r.Vars), "setting")
		if s.query() != "" && row.matching > 0 {
			count = fmt.Sprintf("%d matching", row.matching)
		}
		info := sFaint.Render(count)
		if row.change > 0 {
			info += sPink.Render(fmt.Sprintf("  %d changed", row.change))
		}
		if r.inUse {
			info += sGood.Render("  in use")
		}
		lines = append(lines, bar+mark+" "+lipgloss.NewStyle().Width(nameW).Render(nameStyle.Render(truncate(r.Name, nameW-2)))+
			lipgloss.NewStyle().Width(labelW).Render(sSub.Render(truncate(r.label, labelW-2)))+info)
	}
	if len(rows) == 0 {
		lines = append(lines, sMuted.Render("   nothing matches"))
	}
	listH := max(3, h-lipgloss.Height(foot)-4)
	start := max(0, min(s.roleCursor-listH/2, len(lines)-listH))
	return head + "\n" + filter + "\n\n" + strings.Join(lines[start:min(len(lines), start+listH)], "\n") + foot
}

func (s *tuningScreen) detailView(w int) string {
	v, ok := s.current()
	if !ok {
		return ""
	}
	inner := w - 6
	lines := []string{sTitle.Render(v.Name)}
	if v.Help != "" {
		help := strings.Split(lipgloss.NewStyle().Width(inner).Render(v.Help), "\n")
		if len(help) > 6 {
			help = append(help[:6], "…")
		}
		lines = append(lines, sSub.Render(strings.Join(help, "\n")))
	}
	short := func(val string) string {
		if strings.Count(val, "\n") > 3 {
			val = strings.Join(strings.Split(val, "\n")[:4], "\n") + "\n…"
		}
		return strings.ReplaceAll(truncateLines(val, inner-9), "\n", "\n         ")
	}
	lines = append(lines, "")
	switch {
	case v.PlaybookSet && s.role >= 0 && s.roles[s.role].Name != config.PlaybookRole && v.Playbook != v.Default:
		label := "Playbook "
		if v.Derived {
			label = "Computed "
		}
		lines = append(lines, sMuted.Render(label)+sFaint.Render(short(v.Playbook)),
			sMuted.Render("Role     ")+sFaint.Render(short(v.Default))+sFaint.Render("   (the playbook's group_vars override it)"))
	default:
		label := "Default  "
		if v.Derived {
			label = "Computed "
		}
		lines = append(lines, sMuted.Render(label)+sFaint.Render(short(v.Effective())))
	}
	if s.editing {
		s.editor.SetWidth(inner)
		lines = append(lines, "", s.editor.View())
		if s.editErr != "" {
			lines = append(lines, sBad.Render("✗ "+s.editErr))
		}
	} else if val, over := s.value(v); over {
		lines = append(lines, sMuted.Render("Yours    ")+sPink.Render(strings.ReplaceAll(truncateLines(val, inner-9), "\n", "\n         ")))
	}
	return sPanel.Padding(0, 2).Width(w - 2).Render(strings.Join(lines, "\n"))
}

func truncateLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = truncate(l, max(8, w))
	}
	return strings.Join(lines, "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
