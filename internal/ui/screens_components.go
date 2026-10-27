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
//
// Two pages in one step: the list to pick from, then the settings the
// picked components (and the admin account) need. enter on the list opens
// the settings; esc there returns to the list.

type componentsScreen struct {
	cfg       *config.Config
	selected  map[string]bool
	cat       int
	cursor    int
	offset    int
	filter    textinput.Model
	filtering bool

	details  bool // the settings page is shown
	settings settingsScreen
	saver    *presetSaver
}

func newComponentsScreen() *componentsScreen {
	in := textinput.New()
	in.Prompt = "/ "
	in.PromptStyle = sAccent
	in.Placeholder = "search every component…"
	in.PlaceholderStyle = sFaint
	return &componentsScreen{filter: in}
}

func (s *componentsScreen) name() string { return "Components" }
func (s *componentsScreen) title() string {
	if s.saver != nil {
		return "Save as a preset"
	}
	if s.details {
		return s.settings.title()
	}
	return "Pick what you want on your server"
}
func (s *componentsScreen) typing() bool {
	if s.saver != nil {
		return s.saver.form.typing()
	}
	if s.details {
		return s.settings.typing()
	}
	return s.filtering
}
func (s *componentsScreen) handlesEsc() bool { return s.details || s.saver != nil }
func (s *componentsScreen) activeForm() *form {
	if s.saver != nil {
		return s.saver.form
	}
	if s.details {
		return s.settings.form
	}
	return nil
}

// openDetails shows the settings page.
func (s *componentsScreen) openDetails() tea.Cmd {
	if !s.details {
		s.leave(s.cfg)
		s.details = true
		return s.settings.enter(s.cfg)
	}
	return nil
}

// nextPage is ctrl+n: from the list it opens the settings page.
func (s *componentsScreen) nextPage() (tea.Cmd, bool) {
	if s.details {
		return nil, false
	}
	return s.openDetails(), true
}

func (s *componentsScreen) keys() [][2]string {
	if s.saver != nil {
		return s.saver.keys()
	}
	if s.details {
		return s.settings.keys()
	}
	if s.filtering {
		return [][2]string{{"type", "search"}, {"↑↓", "move"}, {"space", "toggle"}, {"enter", "done"}}
	}
	return [][2]string{{"←→", "category"}, {"↑↓", "move"}, {"space", "toggle"}, {"/", "search"}, {"p", "save as preset"}, {"enter", "next"}}
}

func (s *componentsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg = c
	s.details, s.saver = false, nil
	s.selected = map[string]bool{}
	for k, v := range c.Components {
		s.selected[k] = v
	}
	return nil
}

func (s *componentsScreen) leave(c *config.Config) {
	if s.details {
		s.settings.leave(c)
	}
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
	if s.saver != nil {
		cmd, done := s.saver.update(s.cfg, msg)
		if done {
			s.saver = nil
		}
		return cmd
	}
	if s.details {
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
			s.settings.leave(s.cfg)
			s.details = false
			return nil
		}
		return s.settings.update(msg)
	}
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
	case "p":
		s.leave(s.cfg)
		p, ok := currentPreset(s.cfg)
		s.saver = newPresetSaver(s.cfg, p, ok)
	case "/":
		s.filtering = true
		s.filter.SetValue("")
		return s.filter.Focus()
	case "enter":
		return s.openDetails()
	}
	return nil
}

func (s *componentsScreen) view(w, h int) string {
	if s.saver != nil {
		return s.saver.view(w, h)
	}
	if s.details {
		return s.settings.view(w, h)
	}
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
			tab = lipgloss.NewStyle().Foreground(cOnAccent).Background(cPurple).Bold(true).Padding(0, 2).Render(label)
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
	gap := 1 // the blank line between items
	if tight {
		gap = 0
	}
	blocks := make([]string, len(items))
	for i, it := range items {
		blocks[i] = markRow("comp:"+strconv.Itoa(i), s.row(it, i, i == s.cursor, w), w)
	}
	used := func(from, to int) int {
		n := 0
		for i := from; i <= to; i++ {
			n += lipgloss.Height(blocks[i]) + gap
		}
		return n - gap
	}
	s.offset = min(s.offset, s.cursor)
	for s.offset < s.cursor && used(s.offset, s.cursor) > listH {
		s.offset++
	}
	var rows []string
	n := 0
	for i := s.offset; i < len(items); i++ {
		bh := lipgloss.Height(blocks[i])
		if n > 0 && n+bh+gap > listH {
			break
		}
		if n > 0 && gap > 0 {
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
	if tight {
		return top + "\n" + list
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
// The current one is a card with its full description, DNS name and docs.
func (s *componentsScreen) row(c catalog.Component, i int, cur bool, w int) string {
	box := sFaint.Render("○")
	if s.selected[c.ID] {
		box = sGood.Render("●")
	}
	style := lipgloss.NewStyle().Foreground(cText)
	var tags []string
	if !s.available(c) {
		box = sFaint.Render("⊘")
		style = sFaint
		tags = append(tags, tagChip("Synapse only", cMuted))
	}
	if c.Status != "" {
		tags = append(tags, tagChip(c.Status, cAmber))
	}
	if c.Suggested && s.available(c) {
		tags = append(tags, tagChip("suggested", cMint))
	}
	if len(c.Fields) > 0 && s.available(c) {
		tags = append(tags, tagChip("needs settings", cPink))
	}
	if s.filter.Value() != "" {
		tags = append(tags, tagChip(c.Category, cMuted))
	}
	tagLine := strings.Join(tags, " ")
	boxMark := mark("compbox:"+strconv.Itoa(i), box)

	if !cur {
		line := "  " + boxMark + "  " + style.Render(c.Name) // in line with the card's
		if len(tags) > 0 && lipgloss.Width(line+"   "+tagLine) <= w {
			line += "   " + tagLine
			tagLine = ""
		}
		desc := "     "
		if tagLine != "" { // too narrow to follow the name: they lead the next line
			desc += tagLine + " "
		}
		return truncateANSI(line, w) + "\n" + desc + sFaint.Render(truncate(c.Description, w-lipgloss.Width(desc)))
	}

	// The current one: a card with everything about it.
	cw := min(w, maxFormWidth)
	inner := cw - 4
	head := boxMark + "  " + gradient(truncate(c.Name, inner-3))
	if !s.available(c) {
		head = boxMark + "  " + sFaint.Render(truncate(c.Name, inner-3))
	}
	lines := []string{head}
	if len(tags) > 0 && lipgloss.Width(head+"   "+tagLine) <= inner {
		lines[0] += "   " + tagLine
	} else if len(tags) > 0 {
		lines = append(lines, truncateANSI("   "+tagLine, inner))
	}
	if tight { // keyboard up: one line, no DNS or docs
		lines = append(lines, "   "+sSub.Render(truncate(c.Description, inner-3)))
		return picked(lines, cw)
	}
	for _, l := range strings.Split(sSub.Width(inner-3).Render(c.Description), "\n") {
		lines = append(lines, "   "+l)
	}
	var meta []string
	if c.Subdomain != "" {
		meta = append(meta, sMuted.Render("DNS ")+sPink.Render(c.Subdomain+".<domain>"))
	}
	if c.Doc != "" {
		meta = append(meta, sFaint.Render("docs/"+c.Doc))
	}
	if len(meta) > 0 {
		lines = append(lines, "", "   "+strings.Join(meta, sFaint.Render("   ·   ")))
	}
	return picked(lines, cw)
}

// ── Settings ───────────────────────────────────────────────────────
//
// The second page of Components.

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
	first := true
	for _, comp := range catalog.Components {
		hv := comp.HostVar()
		if !c.Enabled(comp) || hv == "" {
			continue
		}
		def := comp.Subdomain + "." + c.Domain
		key := "host." + comp.ID
		s.vars[key] = hv
		fl := textField(key, comp.Name, c.Values[hv], def, "Default "+def+". Leave empty to keep it, or type any hostname pointed at the server.")
		if first {
			fl.section, first = "Addresses", false
		}
		fields = append(fields, fl)
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
