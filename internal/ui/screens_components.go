package ui

import (
	"fmt"
	"strconv"
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
		label := cat + "  " + fmt.Sprintf("%d/%d", on, n)
		tab := sMuted.Padding(0, 2).Render(label)
		if i == s.cat && s.filter.Value() == "" {
			tab = lipgloss.NewStyle().Foreground(lipgloss.Color("#111018")).Background(cPurple).Bold(true).Padding(0, 2).Render(label)
		}
		tabs = append(tabs, mark("cat:"+strconv.Itoa(i), tab))
	}

	count := 0
	for _, c := range catalog.Components {
		if s.selected[c.ID] && s.available(c) {
			count++
		}
	}
	total := badge(fmt.Sprintf("%d selected", count), cMint)

	top := tabStrip(tabs, s.cat, w-lipgloss.Width(total)-4)
	if s.filtering || s.filter.Value() != "" {
		top = s.filter.View()
	}
	top = mark("compfilter", top)
	top += strings.Repeat(" ", max(2, w-lipgloss.Width(top)-lipgloss.Width(total))) + total

	items := s.items()
	s.cursor = max(0, min(s.cursor, len(items)-1))
	listH := max(3, h-lipgloss.Height(top)-1)

	if len(items) == 0 {
		return top + "\n\n" + sMuted.Render("   nothing matches")
	}

	// Items are separated by a blank line and differ in height (the current
	// one shows its full description), so scroll by whole items.
	blocks := make([]string, len(items))
	for i, it := range items {
		blocks[i] = markLines("comp:"+strconv.Itoa(i), s.row(it, i, i == s.cursor, w))
	}
	used := func(from, to int) int {
		n := 0
		for i := from; i <= to; i++ {
			n += lipgloss.Height(blocks[i]) + 1
		}
		return n - 1
	}
	s.offset = min(s.offset, s.cursor)
	for s.offset < s.cursor && used(s.offset, s.cursor) > listH {
		s.offset++
	}
	var rows []string
	n := 0
	for i := s.offset; i < len(items); i++ {
		bh := lipgloss.Height(blocks[i])
		if n > 0 && n+bh+1 > listH {
			break
		}
		if n > 0 {
			rows = append(rows, "")
			n++
		}
		rows = append(rows, blocks[i])
		n += bh
	}
	list := strings.Join(rows, "\n")
	if s.offset > 0 {
		list = sFaint.Render("   ▲ more") + "\n" + list
	}
	return top + "\n\n" + list
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
		left = mark("key:←", sAccent.Render("‹ "))
	}
	if hi < len(tabs) {
		right = mark("key:→", sAccent.Render(" ›"))
	}
	return left + strings.Join(tabs[lo:hi], "") + right
}

// row renders one component: a name line with tags and a description.
// The current one gets a full description plus its DNS name and docs.
func (s *componentsScreen) row(c catalog.Component, i int, cur bool, w int) string {
	box := sFaint.Render("○")
	if s.selected[c.ID] {
		box = sGood.Render("●")
	}
	style := lipgloss.NewStyle().Foreground(cText)
	if cur {
		style = sTitle
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
	bar := "   "
	if cur {
		bar = sAccent.Render("▌") + "  "
	}
	line := bar + mark("compbox:"+strconv.Itoa(i), box) + "  " + style.Render(c.Name)
	if len(tags) > 0 {
		line += "    " + strings.Join(tags, sFaint.Render("  ·  "))
	}
	lines := []string{truncateANSI(line, w)}

	pad := bar + "   "
	textW := max(20, w-lipgloss.Width(pad)-1)
	if !cur {
		lines = append(lines, pad+sFaint.Render(truncate(c.Description, textW)))
		return strings.Join(lines, "\n")
	}
	desc := sSub.Width(textW).Render(c.Description)
	for _, l := range strings.Split(desc, "\n") {
		lines = append(lines, pad+l)
	}
	var meta []string
	if c.Subdomain != "" {
		meta = append(meta, sMuted.Render("DNS ")+sPink.Render(c.Subdomain+".<domain>"))
	}
	if c.Doc != "" {
		meta = append(meta, sFaint.Render("docs/"+c.Doc))
	}
	if len(meta) > 0 {
		lines = append(lines, truncateANSI(pad+strings.Join(meta, sFaint.Render("   ·   ")), w))
	}
	return strings.Join(lines, "\n")
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
	if c := s.form.current(); c != nil && c.kind == fArea {
		return [][2]string{{"enter", "new line"}, {"tab", "leave box"}, {"ctrl+n", "next step"}}
	}
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
