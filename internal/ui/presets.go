package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
)

// ── Homeserver ─────────────────────────────────────────────────────
//
// The step starts with the presets, whole stacks to start from, and then
// the homeservers alone for picking the components yourself. The rows are
// one list: presets first, then homeservers.

type homeserverScreen struct {
	cfg     *config.Config
	presets []config.Preset
	sel     int // the row under the cursor
	hs      int // the homeserver chosen
	armed   string
	saver   *presetSaver
	err     string
}

// noteMsg puts a line on the status bar.
type noteMsg string

func (s *homeserverScreen) name() string { return "Homeserver" }
func (s *homeserverScreen) title() string {
	if s.saver != nil {
		return "Save as a preset"
	}
	return "Choose your homeserver"
}
func (s *homeserverScreen) typing() bool     { return s.saver != nil && s.saver.form.typing() }
func (s *homeserverScreen) handlesEsc() bool { return s.saver != nil || s.armed != "" }
func (s *homeserverScreen) activeForm() *form {
	if s.saver != nil {
		return s.saver.form
	}
	return nil
}

func (s *homeserverScreen) keys() [][2]string {
	if s.saver != nil {
		return s.saver.keys()
	}
	if s.armed != "" {
		return [][2]string{{s.armedKey(), "again to confirm"}, {"esc", "cancel"}}
	}
	keys := [][2]string{{"↑↓", "select"}, {"enter", "choose"}, {"s", "save as preset"}}
	if p, ok := s.preset(); ok && (p.Edited || !p.Builtin) {
		keys = append(keys, [2]string{"d", map[bool]string{true: "reset", false: "delete"}[p.Edited]})
	}
	return keys
}

func (s *homeserverScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.saver, s.armed, s.err = c, nil, "", ""
	s.reload()
	for i, h := range catalog.Homeservers {
		if h.ID == c.Homeserver {
			s.hs = i
		}
	}
	s.sel = len(s.presets) + s.hs
	for i, p := range s.presets {
		if p.Matches(c) {
			s.sel = i
			break
		}
	}
	return nil
}

func (s *homeserverScreen) reload() {
	var err error
	s.presets, err = config.Presets()
	if err != nil {
		s.err = "your presets couldn't be read: " + err.Error()
	}
	s.sel = min(s.sel, len(s.presets)+len(catalog.Homeservers)-1)
}

func (s *homeserverScreen) leave(c *config.Config) {
	hs := catalog.Homeservers[s.hs].ID
	if hs == "synapse" && c.Homeserver != "synapse" && !c.Installed() {
		// Synapse logins go through MAS unless it's unticked on Components.
		// An installed server keeps what it has: moving accounts to MAS
		// is a migration of its own.
		c.Components["mas"] = true
	}
	c.Homeserver = hs
}

// preset is the preset under the cursor, if it's on one.
func (s *homeserverScreen) preset() (config.Preset, bool) {
	if s.sel < len(s.presets) {
		return s.presets[s.sel], true
	}
	return config.Preset{}, false
}

func (s *homeserverScreen) armedKey() string {
	if strings.HasPrefix(s.armed, "apply:") {
		return "enter"
	}
	return "d"
}

// custom reports whether the components ticked now are more than a
// preset or the defaults, so replacing them should be confirmed.
func (s *homeserverScreen) custom() bool {
	for _, p := range s.presets {
		if p.Matches(s.cfg) {
			return false
		}
	}
	return !slices.Equal(s.cfg.Stack().Components, config.Fresh().Stack().Components)
}

// choose acts on the row under the cursor: a preset replaces the stack, a
// homeserver is picked on its own.
func (s *homeserverScreen) choose() tea.Cmd {
	p, ok := s.preset()
	if !ok {
		s.hs = s.sel - len(s.presets)
		return nav(1)
	}
	if s.custom() && s.armed != "apply:"+p.ID {
		s.armed = "apply:" + p.ID
		return nil
	}
	s.armed = ""
	s.cfg.ApplyPreset(p.Preset)
	for i, h := range catalog.Homeservers {
		if h.ID == p.Homeserver {
			s.hs = i
		}
	}
	note := noteMsg(fmt.Sprintf("✓ started from %s: change anything here, or enter to fill in the settings", p.Name))
	return tea.Batch(nav(1), func() tea.Msg { return note })
}

func (s *homeserverScreen) update(msg tea.Msg) tea.Cmd {
	if s.saver != nil {
		cmd, done := s.saver.update(s.cfg, msg)
		if done {
			s.saver = nil
			s.reload()
		}
		return cmd
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	key := k.String()
	if s.armed != "" && key != s.armedKey() {
		s.armed = ""
		if key == "esc" {
			return nil
		}
	}
	n := len(s.presets) + len(catalog.Homeservers)
	switch key {
	case "up", "k", "shift+tab":
		s.sel = (s.sel + n - 1) % n
	case "down", "j", "tab":
		s.sel = (s.sel + 1) % n
	case "enter", " ":
		return s.choose()
	case "s":
		s.leave(s.cfg)
		p, ok := s.preset()
		if !ok {
			p, ok = currentPreset(s.cfg)
		}
		s.saver = newPresetSaver(s.cfg, p, ok)
	case "d":
		p, ok := s.preset()
		if !ok || (p.Builtin && !p.Edited) {
			return nil
		}
		if s.armed != "delete:"+p.ID {
			s.armed = "delete:" + p.ID
			return nil
		}
		s.armed = ""
		if err := config.DeletePreset(p.ID); err != nil {
			s.err = err.Error()
			return nil
		}
		s.reload()
		if p.Edited {
			return func() tea.Msg { return noteMsg("✓ " + p.Name + " is back to how interzone ships it") }
		}
		return func() tea.Msg { return noteMsg("✓ deleted the preset " + p.Name) }
	}
	if s.sel >= len(s.presets) {
		s.hs = s.sel - len(s.presets)
	}
	return nil
}

func (s *homeserverScreen) click(id string) (tea.Cmd, bool) {
	if s.saver != nil {
		return nil, false
	}
	if _, again, ok := rowClick(id, "preset:", &s.sel, len(s.presets)); ok {
		if again || s.armed != "" {
			return s.choose(), true
		}
		return nil, true // the first click shows what's in it
	}
	sel := s.sel - len(s.presets)
	if _, _, ok := rowClick(id, "hs:", &sel, len(catalog.Homeservers)); ok {
		s.sel, s.hs = len(s.presets)+sel, sel
		return nav(1), true
	}
	return nil, false
}

func (s *homeserverScreen) view(w, h int) string {
	if s.saver != nil {
		return s.saver.view(w, h)
	}
	rows := s.presetRows(w)
	top := rows
	if s.err != "" {
		top = sBad.Width(w).Render("✗ "+s.err) + "\n\n" + top
	}
	gap := "\n\n"
	if tight {
		gap = "\n"
	}
	head := gap + lipgloss.NewStyle().Width(min(w, 84)).Render(sSection.Render("Or only the homeserver")+sMuted.Render(", and pick the components yourself")) + gap
	room := h - lipgloss.Height(top) - lipgloss.Height(head)
	full := s.cards(w, false)
	if lipgloss.Height(full) > room {
		// Short screen: only the chosen homeserver gets a card.
		full = s.cards(w, true)
	}
	out := top + head + full
	if lipgloss.Height(out) <= h {
		return out
	}
	// Still too tall: scroll to keep the row under the cursor in view.
	cur := lipgloss.Height(top) - lipgloss.Height(rows) + 1 + s.sel // past the error and the heading
	if s.sel >= len(s.presets) {
		cur = lipgloss.Height(top) + lipgloss.Height(head) + s.sel - len(s.presets)
	}
	return windowAround(out, cur, h)
}

// windowAround cuts s to h lines, keeping line cur in view with some
// context, and says which way there's more.
func windowAround(s string, cur, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= h || h < 3 {
		return s
	}
	start := max(0, min(cur-h/3, len(lines)-h))
	view := append([]string(nil), lines[start:start+h]...)
	if start > 0 {
		view[0] = sFaint.Render("  ▲ more above")
	}
	if start+h < len(lines) {
		view[h-1] = sFaint.Render("  ▼ more below")
	}
	return strings.Join(view, "\n")
}

// presetRows lists the presets, with what's in the one under the cursor.
func (s *homeserverScreen) presetRows(w int) string {
	tw := min(w, 84)
	lines := []string{sSection.Render("Start from a preset")}
	nameW := 4
	for _, p := range s.presets {
		nameW = max(nameW, lipgloss.Width(p.Name))
	}
	nameW = min(nameW, max(10, tw/2-4))
	for i, p := range s.presets {
		hs := p.Homeserver
		for _, h := range catalog.Homeservers {
			if h.ID == p.Homeserver {
				hs = h.Name
			}
		}
		pointer, name := "  ", sTitle.Render(truncate(p.Name, nameW))
		if i == s.sel {
			pointer, name = sAccent.Render("› "), gradient(truncate(p.Name, nameW))
		}
		tags := ""
		switch {
		case s.armed == "apply:"+p.ID:
			tags = "  " + sWarn.Render("enter again replaces your components")
		case s.armed == "delete:"+p.ID && p.Edited:
			tags = "  " + sWarn.Render("d again brings interzone's back")
		case s.armed == "delete:"+p.ID:
			tags = "  " + sWarn.Render("d again deletes it")
		default:
			if p.Matches(s.cfg) {
				tags += "  " + sGood.Render("● now")
			}
			if p.Edited {
				tags += "  " + sPink.Render("edited")
			} else if !p.Builtin {
				tags += "  " + sPink.Render("yours")
			}
		}
		pad := strings.Repeat(" ", max(0, nameW-lipgloss.Width(truncate(p.Name, nameW))))
		row := pointer + name + pad + "  " + sMuted.Render(fmt.Sprintf("%s · %d components", hs, len(p.Components))) + tags
		lines = append(lines, markRow("preset:"+strconv.Itoa(i), truncateANSI(row, w), w))
		if i == s.sel {
			id := "preset:" + strconv.Itoa(i)
			if tight {
				lines = append(lines, markRow(id, "    "+sSub.Render(truncate(presetSummary(p.Preset), tw-4)), w))
				continue
			}
			lines = append(lines, markLines(id, indent(presetCard(p.Preset, tw-2), "  ")))
		}
	}
	return strings.Join(lines, "\n")
}

// categoryLabels are the short headings a preset's components are
// grouped under.
var categoryLabels = map[string]string{
	"Core services": "Core", "Clients": "Apps", "Authentication": "Login", "Administration": "Admin",
	"Bots": "Bots", "Bridges": "Bridges", "Calls & extras": "Calls", "File storage": "Files",
}

// presetGroups sorts a preset's components into the catalog's categories.
func presetGroups(p catalog.Preset) []bulletGroup {
	var groups []bulletGroup
	for _, cat := range catalog.Categories {
		g := bulletGroup{label: categoryLabels[cat]}
		if g.label == "" {
			g.label = cat
		}
		for _, id := range p.Components {
			for _, c := range catalog.Components {
				if c.ID == id && c.Category == cat {
					g.items = append(g.items, c.Name)
				}
			}
		}
		if len(g.items) > 0 {
			groups = append(groups, g)
		}
	}
	if p.Backups {
		groups = append(groups, bulletGroup{label: "Backups", items: []string{"Daily database dumps"}})
	}
	return groups
}

// presetSummary is a preset on one line, for short screens.
func presetSummary(p catalog.Preset) string {
	var names []string
	for _, g := range presetGroups(p) {
		names = append(names, g.items...)
	}
	if p.Description == "" {
		return strings.Join(names, " · ")
	}
	return p.Description + " " + strings.Join(names, " · ")
}

// presetCard is what's in the preset under the cursor: its pitch, and the
// components grouped under small headings.
func presetCard(p catalog.Preset, w int) string {
	inner := w - 4
	var lines []string
	if p.Description != "" {
		lines = append(lines, strings.Split(sSub.Width(inner).Render(p.Description), "\n")...)
		lines = append(lines, "")
	}
	lines = append(lines, strings.Split(bullets(presetGroups(p), inner), "\n")...)
	return picked(lines, w)
}

// indent puts pad before every line of s.
func indent(s, pad string) string {
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

func (s *homeserverScreen) cards(w int, compact bool) string {
	cardW := min(w, 84)
	card := sPanel.Padding(0, 2).Width(cardW - 2)
	var cards []string
	for i, hs := range catalog.Homeservers {
		on := s.sel == len(s.presets)+i
		tag := sMuted.Render("   " + hs.Tagline)
		if i != s.hs && !on && compact {
			cards = append(cards, markRow("hs:"+strconv.Itoa(i), "   "+sFaint.Render("○  ")+sMuted.Render(hs.Name)+tag, w))
			continue
		}
		style, name := card, sFaint.Render("○  ")+sTitle.Render(hs.Name)
		if i == s.hs {
			name = sAccent.Render("◉  ") + sTitle.Render(hs.Name)
		}
		if on {
			style, name = card.BorderForeground(cPurple), sAccent.Render("◉  ")+gradient(hs.Name)
		}
		head := name + tag
		if lipgloss.Width(head) > cardW-6 { // the tagline gets its own line
			head = name + "\n   " + sMuted.Render(hs.Tagline)
		}
		desc := sSub.Width(cardW - 9).Render(hs.Description)
		cards = append(cards, markLines("hs:"+strconv.Itoa(i), style.Render(head+"\n\n   "+strings.ReplaceAll(desc, "\n", "\n   "))))
	}
	sep := "\n\n"
	if compact {
		sep = "\n"
	}
	return strings.Join(cards, sep)
}

// ── Saving a preset ────────────────────────────────────────────────
//
// The Homeserver and Components steps both save the current homeserver
// and ticked components as a preset. Settings and secrets stay out of it.

// presetSaving screens keep their form's enter while saving a preset,
// instead of it moving on to the next step.
type presetSaving interface{ savingPreset() bool }

func (s *homeserverScreen) savingPreset() bool { return s.saver != nil }
func (s *componentsScreen) savingPreset() bool { return s.saver != nil }

type presetSaver struct {
	form    *form
	replace string // the name enter replaces, once warned
	err     string
	count   int
}

// newPresetSaver starts the form, filled in from p if there is one.
func newPresetSaver(c *config.Config, p config.Preset, ok bool) *presetSaver {
	name, desc := "", ""
	if ok {
		name, desc = p.Name, p.Description
	}
	return &presetSaver{
		form: newForm(
			textField("name", "Name", name, "My stack", "Saving under an existing preset's name changes that preset."),
			textField("desc", "Description", desc, "what it's for", "Shown under the name on the Homeserver step."),
		),
		count: len(c.Stack().Components),
	}
}

// currentPreset is the preset c was last started from, if it still exists.
func currentPreset(c *config.Config) (config.Preset, bool) {
	ps, _ := config.Presets()
	for _, p := range ps {
		if p.ID == c.Preset {
			return p, true
		}
	}
	return config.Preset{}, false
}

func (p *presetSaver) keys() [][2]string {
	return [][2]string{{"tab/↑↓", "move"}, {"enter", "save"}, {"esc", "cancel"}}
}

// update returns done once the preset is saved or the form cancelled.
func (p *presetSaver) update(c *config.Config, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case formDoneMsg:
		name := strings.TrimSpace(p.form.value("name"))
		if name == "" {
			p.err = "give it a name"
			return nil, false
		}
		if old, ok := config.PresetNamed(name); ok && !strings.EqualFold(p.replace, name) {
			p.replace = name
			p.err = "There's already a preset called " + old.Name + ". Press enter again to replace what's in it."
			return nil, false
		}
		st := c.Stack()
		st.Name, st.Description = name, strings.TrimSpace(p.form.value("desc"))
		saved, err := config.SavePreset(st)
		if err != nil {
			p.err = err.Error()
			return nil, false
		}
		c.Preset = saved.ID
		return func() tea.Msg { return noteMsg("✓ saved the preset " + saved.Name) }, true
	case tea.KeyMsg:
		if m.String() == "esc" {
			return nil, true
		}
	}
	return p.form.Update(msg), false
}

func (p *presetSaver) view(w, h int) string {
	tw := min(w, maxFormWidth)
	intro := sSub.Width(tw).Render(fmt.Sprintf("Saves the homeserver and the %d ticked components, without their settings or secrets. It's offered on the Homeserver step for every server.", p.count))
	foot := ""
	if p.err != "" {
		foot = "\n\n" + sWarn.Width(tw).Render("! "+p.err)
	}
	return intro + "\n\n" + p.form.View(w, h-lipgloss.Height(intro)-lipgloss.Height(foot)-2) + foot
}
