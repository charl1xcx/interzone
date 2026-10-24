package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
)

// ── Components ─────────────────────────────────────────────────────

type componentsScreen struct {
	cfg       *config.Config
	selected  map[string]bool
	cat       int
	cursor    int
	offset    int
	filter    textinput.Model
	filtering bool
}

func newComponentsScreen() *componentsScreen {
	in := textinput.New()
	in.Prompt = "/ "
	in.PromptStyle = sAccent
	in.Placeholder = "search every component…"
	in.PlaceholderStyle = sFaint
	return &componentsScreen{filter: in}
}

func (s *componentsScreen) name() string  { return "Components" }
func (s *componentsScreen) title() string { return "Pick what you want on your server" }
func (s *componentsScreen) typing() bool  { return s.filtering }
func (s *componentsScreen) keys() [][2]string {
	if s.filtering {
		return [][2]string{{"type", "search"}, {"↑↓", "move"}, {"space", "toggle"}, {"enter", "done"}}
	}
	return [][2]string{{"←→", "category"}, {"↑↓", "move"}, {"space", "toggle"}, {"/", "search"}, {"enter", "next"}}
}

func (s *componentsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.selected = map[string]bool{}
	for k, v := range c.Components {
		s.selected[k] = v
	}
	return nil
}

func (s *componentsScreen) leave(c *config.Config) {
	c.Components = s.selected
	c.FillGenerated()
}

// items returns what the list currently shows: a category, or search results.
func (s *componentsScreen) items() []catalog.Component {
	var out []catalog.Component
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	for _, c := range catalog.Components {
		if q != "" {
			if strings.Contains(strings.ToLower(c.Name+" "+c.Description+" "+c.Category), q) {
				out = append(out, c)
			}
		} else if c.Category == catalog.Categories[s.cat] {
			out = append(out, c)
		}
	}
	return out
}

func (s *componentsScreen) available(c catalog.Component) bool {
	return !c.SynapseOnly || s.cfg.Homeserver == "synapse"
}

func (s *componentsScreen) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	items := s.items()
	switch k.String() {
	case "up", "ctrl+k":
		s.cursor = max(0, s.cursor-1)
		return nil
	case "down", "ctrl+j":
		s.cursor = min(len(items)-1, s.cursor+1)
		return nil
	case " ", "ctrl+@":
		if s.cursor < len(items) {
			it := items[s.cursor]
			if s.available(it) {
				s.selected[it.ID] = !s.selected[it.ID]
			}
		}
		return nil
	}
	if s.filtering {
		switch k.String() {
		case "enter", "esc":
			s.filtering = false
			s.filter.Blur()
			if k.String() == "esc" {
				s.filter.SetValue("")
				s.cursor = 0
			}
			return nil
		}
		var cmd tea.Cmd
		s.filter, cmd = s.filter.Update(msg)
		s.cursor = 0
		return cmd
	}
	switch k.String() {
	case "esc":
		s.filter.SetValue("")
		s.cursor, s.offset = 0, 0
	case "left", "h", "shift+tab":
		s.filter.SetValue("")
		s.cat = (s.cat + len(catalog.Categories) - 1) % len(catalog.Categories)
		s.cursor, s.offset = 0, 0
	case "right", "l", "tab":
		s.filter.SetValue("")
		s.cat = (s.cat + 1) % len(catalog.Categories)
		s.cursor, s.offset = 0, 0
	case "k":
		s.cursor = max(0, s.cursor-1)
	case "j":
		s.cursor = min(len(items)-1, s.cursor+1)
	case "/":
		s.filtering = true
		s.filter.SetValue("")
		return s.filter.Focus()
	case "enter":
		return nav(1)
	}
	return nil
}

func (s *componentsScreen) view(w, h int) string {
	// Category tabs.
	var tabs []string
	for i, cat := range catalog.Categories {
		n, on := 0, 0
		for _, c := range catalog.Components {
			if c.Category == cat {
				n++
				if s.selected[c.ID] && s.available(c) {
					on++
				}
			}
		}
		label := fmt.Sprintf("%s %d/%d", cat, on, n)
		if i == s.cat && s.filter.Value() == "" {
			tabs = append(tabs, lipgloss.NewStyle().Foreground(lipgloss.Color("#111018")).Background(cPurple).Bold(true).Padding(0, 1).Render(label))
		} else {
			tabs = append(tabs, sMuted.Padding(0, 1).Render(label))
		}
	}
	tabLine := tabStrip(tabs, s.cat, w-14)

	top := tabLine
	if s.filtering || s.filter.Value() != "" {
		top = s.filter.View()
	}

	items := s.items()
	s.cursor = max(0, min(s.cursor, len(items)-1))

	detail := s.detail(items, w)
	listH := max(3, h-lipgloss.Height(top)-lipgloss.Height(detail)-2)
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+listH {
		s.offset = s.cursor - listH + 1
	}

	var rows []string
	for i := s.offset; i < len(items) && i < s.offset+listH; i++ {
		rows = append(rows, s.row(items[i], i == s.cursor, w))
	}
	if len(items) == 0 {
		rows = append(rows, sMuted.Render("  nothing matches"))
	}
	list := lipgloss.NewStyle().Height(listH).Render(strings.Join(rows, "\n"))

	count := 0
	for _, c := range catalog.Components {
		if s.selected[c.ID] && s.available(c) {
			count++
		}
	}
	total := sMuted.Render(fmt.Sprintf("%d selected", count))
	return lipgloss.JoinVertical(lipgloss.Left, top+"  "+total, "", list, detail)
}

// tabStrip fits the tabs on one line, keeping the current tab visible and
// marking hidden ones with arrows.
func tabStrip(tabs []string, cur, w int) string {
	lo, hi := cur, cur+1
	width := lipgloss.Width(tabs[cur]) + 4
	for {
		grew := false
		if hi < len(tabs) && width+lipgloss.Width(tabs[hi]) <= w {
			width += lipgloss.Width(tabs[hi])
			hi++
			grew = true
		}
		if lo > 0 && width+lipgloss.Width(tabs[lo-1]) <= w {
			lo--
			width += lipgloss.Width(tabs[lo])
			grew = true
		}
		if !grew {
			break
		}
	}
	left, right := "  ", "  "
	if lo > 0 {
		left = sAccent.Render("‹ ")
	}
	if hi < len(tabs) {
		right = sAccent.Render(" ›")
	}
	return left + strings.Join(tabs[lo:hi], "") + right
}

func (s *componentsScreen) row(c catalog.Component, cur bool, w int) string {
	box := sFaint.Render("○")
	if s.selected[c.ID] {
		box = sGood.Render("●")
	}
	name := c.Name
	style := sTitle
	if !cur {
		style = lipgloss.NewStyle().Foreground(cText)
	}
	var tags []string
	if !s.available(c) {
		box = sFaint.Render("⊘")
		style = sFaint
		tags = append(tags, sFaint.Render("Synapse only"))
	}
	if c.Status != "" {
		tags = append(tags, sWarn.Render(c.Status))
	}
	if len(c.Fields) > 0 && s.available(c) {
		tags = append(tags, sFaint.Render("needs settings"))
	}
	if s.filter.Value() != "" {
		tags = append(tags, sFaint.Render(c.Category))
	}
	pointer := "  "
	if cur {
		pointer = sAccent.Render("› ")
	}
	line := pointer + box + " " + style.Render(name)
	if len(tags) > 0 {
		line += "  " + strings.Join(tags, sFaint.Render(" · "))
	}
	return truncateANSI(line, w)
}

func (s *componentsScreen) detail(items []catalog.Component, w int) string {
	if len(items) == 0 {
		return ""
	}
	c := items[s.cursor]
	body := sTitle.Render(c.Name) + "\n" + sSub.Render(c.Description)
	if c.Subdomain != "" {
		body += "\n" + sMuted.Render("Needs DNS: ") + sPink.Render(c.Subdomain+".<domain>")
	}
	body += "\n" + sFaint.Render("docs/"+c.Doc)
	return sPanel.Width(w - 2).Render(body)
}

// ── Settings ───────────────────────────────────────────────────────

type settingsScreen struct {
	form *form
	vars map[string]string // form key → vars.yml variable
}

func (s *settingsScreen) name() string  { return "Settings" }
func (s *settingsScreen) title() string { return "Fill in the details" }
func (s *settingsScreen) typing() bool  { return s.form.typing() }
func (s *settingsScreen) keys() [][2]string {
	return [][2]string{{"tab/↑↓", "move"}, {"space", "toggle"}, {"ctrl+r", "reveal secrets"}, {"enter", "next"}}
}

func (s *settingsScreen) enter(c *config.Config) tea.Cmd {
	c.FillGenerated()
	var fields []*field
	switch c.Homeserver {
	case "synapse", "dendrite":
		u := textField("admin.user", "Admin username", c.AdminUser, "admin", "Created automatically after install.")
		u.section = "Your admin account"
		fields = append(fields, u, secretField("admin.pass", "Admin password", c.AdminPassword, "Generated for you. Press ctrl+r to see it."))
	case "continuwuity", "tuwunel":
		t := secretField("admin.pass", "Registration token", c.AdminPassword,
			"Use this token in any client to register your first account.")
		t.section = "Your admin account"
		fields = append(fields, t)
	}

	s.vars = map[string]string{}
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) || len(comp.Fields) == 0 {
			continue
		}
		for i, f := range comp.Fields {
			key := comp.ID + "." + f.Var
			s.vars[key] = f.Var
			v := c.Values[f.Var]
			var fl *field
			switch f.Kind {
			case catalog.Bool:
				fl = toggleField(key, f.Label, v == "true", f.Help)
			case catalog.Secret, catalog.Hex64:
				fl = secretField(key, f.Label, v, f.Help)
			case catalog.YAML:
				fl = areaField(key, f.Label, v, f.Help)
			case catalog.List:
				fl = textField(key, f.Label, v, "comma, separated, values", f.Help)
			default:
				fl = textField(key, f.Label, v, f.Default, f.Help)
			}
			fl.required = f.Required
			if f.ShowIf != "" {
				dep := comp.ID + "." + f.ShowIf
				fl.hidden = func() bool { return s.form.value(dep) != "true" }
			}
			if i == 0 {
				fl.section = comp.Name
			}
			fields = append(fields, fl)
		}
	}
	extra := areaField("extra", "Extra vars.yml lines", c.ExtraYAML,
		"Anything else from the MDAD docs. Written at the end of vars.yml as-is.")
	extra.section = "Advanced"
	fields = append(fields, extra)
	s.form = newForm(fields...)
	return nil
}

func (s *settingsScreen) leave(c *config.Config) {
	f := s.form
	if fl := f.get("admin.user"); fl != nil {
		c.AdminUser = fl.Value()
	}
	if fl := f.get("admin.pass"); fl != nil {
		c.AdminPassword = fl.Value()
	}
	for key, v := range s.vars {
		c.Values[v] = f.value(key)
	}
	c.ExtraYAML = f.value("extra")
}

func (s *settingsScreen) update(msg tea.Msg) tea.Cmd { return s.form.Update(msg) }
func (s *settingsScreen) view(w, h int) string {
	return s.form.View(w, h)
}
