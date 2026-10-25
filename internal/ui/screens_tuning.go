package ui

import (
	"context"
	"fmt"
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
// Every variable the homeserver's role defines, with its default and the
// comment the playbook documents it with. Changed values are saved in
// Config.Overrides and written into vars.yml.

type defaultsMsg struct {
	vars []config.RoleVar
	err  error
}

type tuningScreen struct {
	cfg  *config.Config
	vars []config.RoleVar
	err  error

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
	return &tuningScreen{spin: sp, filter: f}
}

func (s *tuningScreen) name() string { return "Tuning" }
func (s *tuningScreen) title() string {
	return "Fine-tune " + homeserverName(s.cfg.Homeserver) + " (optional)"
}
func (s *tuningScreen) typing() bool     { return s.filtering || s.editing }
func (s *tuningScreen) handlesEsc() bool { return s.editing || s.filtering || s.filter.Value() != "" }
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
		return [][2]string{{"f", "download the playbook"}, {"ctrl+n", "skip"}}
	}
	return [][2]string{{"↑↓", "move"}, {"enter", "change"}, {"del", "reset"}, {"/", "filter"}, {"c", "changed only"}, {"ctrl+n", "next step"}}
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
	s.vars, s.err = config.HomeserverDefaults(s.cfg.PlaybookDir, s.cfg.Homeserver)
	s.cursor = min(s.cursor, max(0, len(s.items())-1))
}

func (s *tuningScreen) leave(*config.Config) { s.editing, s.filtering = false, false }

// items is the filtered list.
func (s *tuningScreen) items() []config.RoleVar {
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	var out []config.RoleVar
	for _, v := range s.vars {
		if _, changed := s.cfg.Overrides[v.Name]; s.onlyChanged && !changed {
			continue
		}
		if q != "" && !strings.Contains(v.Name, q) && !strings.Contains(strings.ToLower(v.Help), q) {
			continue
		}
		out = append(out, v)
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
	return v.Default, false
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
	if val == v.Default {
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
	n := len(s.items())
	switch k.String() {
	case "esc":
		s.filter.SetValue("")
		s.cursor = 0
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

func (s *tuningScreen) view(w, h int) string {
	if s.fetching {
		return s.spin.View() + sMuted.Render(" downloading the playbook…")
	}
	if s.err != nil {
		return sWarn.Render("The playbook isn't downloaded yet, so its settings can't be listed.") + "\n\n" +
			sMuted.Render("Press ") + sKey.Render("f") + sMuted.Render(" to download it from "+s.cfg.Sources.PlaybookGit+",") + "\n" +
			sMuted.Render("or skip this optional step with ") + sKey.Render("ctrl+n") + sMuted.Render(".") + "\n\n" +
			sFaint.Render(s.err.Error())
	}
	w = min(w, 120)
	items := s.items()
	changed := len(s.cfg.Overrides)
	head := sMuted.Render(fmt.Sprintf("%d settings from the playbook's %s role", len(s.vars), config.HomeserverRole[s.cfg.Homeserver]))
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
	def := v.Default
	if strings.Count(def, "\n") > 3 {
		def = strings.Join(strings.Split(def, "\n")[:4], "\n") + "\n…"
	}
	label := "Default  "
	if v.Derived {
		label = "Computed "
	}
	lines = append(lines, "", sMuted.Render(label)+sFaint.Render(strings.ReplaceAll(truncateLines(def, inner-9), "\n", "\n         ")))
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
