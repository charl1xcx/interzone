package ui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/catalog"
	"interzone/internal/config"
)

// ── Import ─────────────────────────────────────────────────────────
//
// Servers › i takes over a server set up with matrix-docker-ansible-deploy
// by hand: ask where its files are, show what was found, and on enter
// save it as a new server and switch to it.

// importOpenMsg opens the import form on the Servers screen.
type importOpenMsg struct{}

type importReadMsg struct {
	im  *config.Import
	err error
}

type importSavedMsg struct {
	name string
	cfg  *config.Config
	err  error
}

type importState struct {
	form *form
	im   *config.Import // read, waiting for enter
	name string
	busy string // what's happening, while it happens
	err  string
}

func (s *serversScreen) startImport() {
	path := textField("path", "Where is it?", "", "~/matrix-docker-ansible-deploy",
		"The playbook folder you ran it from, its inventory folder, or the server's vars.yml. Only read; interzone works on a copy.")
	path.required = true
	name := textField("name", "Name in interzone", "", "from the domain",
		"Lowercase letters, digits, dots and dashes. Leave empty to name it after the domain.")
	s.imp = &importState{form: newForm(path, name)}
}

func (s *serversScreen) importKeys() [][2]string {
	switch {
	case s.imp.busy != "":
		return nil
	case s.imp.im != nil:
		return [][2]string{{"enter", "import it"}, {"esc", "back"}}
	}
	return [][2]string{{"tab/↑↓", "move"}, {"enter", "read it"}, {"esc", "cancel"}}
}

func (s *serversScreen) importUpdate(msg tea.Msg) tea.Cmd {
	st := s.imp
	switch m := msg.(type) {
	case importReadMsg:
		st.busy = ""
		if m.err != nil {
			st.err = m.err.Error()
			return nil
		}
		st.im, st.err = m.im, ""
		if st.name == "" {
			st.name = config.SuggestName(m.im.Config.Domain)
		}
		return nil
	case importSavedMsg:
		st.busy = ""
		if m.err != nil {
			st.err = m.err.Error()
			return nil
		}
		s.imp = nil
		note := "✓ imported " + m.cfg.Domain + " as " + m.name
		return func() tea.Msg { return switchServerMsg{name: m.name, cfg: m.cfg, note: note} }
	case formDoneMsg:
		st.name = strings.ToLower(strings.TrimSpace(st.form.value("name")))
		if st.name != "" {
			if err := config.ValidProfileName(st.name); err != nil {
				st.err = "name: " + err.Error()
				return nil
			}
		}
		st.busy, st.err = "reading it…", ""
		path, base := st.form.value("path"), *s.cfg
		return func() tea.Msg {
			im, err := config.ImportMDAD(path, &base)
			return importReadMsg{im, err}
		}
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok || st.busy != "" {
		return nil
	}
	if st.im != nil {
		switch k.String() {
		case "esc":
			st.im = nil // back to the form
		case "enter":
			im, name := st.im, st.name
			st.busy = "saving it…"
			if im.PlaybookDir != "" {
				st.busy = "copying the playbook…"
			}
			return func() tea.Msg {
				err := im.Save(name)
				return importSavedMsg{name, im.Config, err}
			}
		}
		return nil
	}
	if k.String() == "esc" {
		s.imp = nil
		return nil
	}
	return st.form.Update(msg)
}

func (s *serversScreen) importView(w, h int) string {
	st := s.imp
	foot := ""
	switch {
	case st.busy != "":
		foot = sMuted.Render(st.busy)
	case st.err != "":
		foot = sBad.Width(min(w, maxFormWidth)).Render("✗ " + st.err)
	}
	if st.im == nil {
		intro := sSub.Width(min(w, maxFormWidth)).Render("Set up a server with matrix-docker-ansible-deploy yourself? interzone can take it over: it reads your vars.yml and inventory and keeps every setting and secret as it is.")
		out := intro + "\n\n" + st.form.View(w, h-lipgloss.Height(intro)-lipgloss.Height(foot)-4)
		if foot != "" {
			out += "\n\n" + foot
		}
		return out
	}
	return importSummary(st.im, st.name, w, h-lipgloss.Height(foot)-2) + "\n\n" + foot
}

// tildePath writes a path under the home directory as ~/….
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home+"/"); ok {
			return "~/" + rest
		}
	}
	return p
}

// importSummary shows what an import found and what it does, cut to h lines.
func importSummary(im *config.Import, name string, w, h int) string {
	c := im.Config
	hs := c.Homeserver
	for _, x := range catalog.Homeservers {
		if x.ID == c.Homeserver {
			hs = x.Name
		}
	}
	tw := min(w, maxFormWidth)
	kv := func(k, v string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(13).Render(sMuted.Render(k)),
			lipgloss.NewStyle().Width(max(10, tw-13)).Render(v))
	}
	lines := []string{
		sTitle.Render(c.Domain),
		"",
		kv("Homeserver", hs),
		kv("Server", im.Where()),
		kv("Components", strings.Join(im.ComponentNames(), ", ")),
	}
	if im.PlaybookDir != "" {
		lines = append(lines, kv("Playbook", "a copy of "+tildePath(im.PlaybookDir)))
	}
	lines = append(lines, kv("Saved as", name))
	list := func(style lipgloss.Style, title string, keys []string) {
		if len(keys) == 0 {
			return
		}
		lines = append(lines, "", style.Render(fmt.Sprintf(title, len(keys))))
		for i, k := range keys {
			if i == 4 && len(keys) > 5 {
				lines = append(lines, sFaint.Render(fmt.Sprintf("   …and %d more", len(keys)-4)))
				break
			}
			lines = append(lines, sFaint.Render("   "+truncate(k, tw-3)))
		}
	}
	list(sGood, "✓ %d kept as you wrote them (interzone has no setting for them)", im.Kept)
	list(sGood, "✓ %d interzone also sets, kept at your values", im.Override)
	list(sGood, "✓ %d interzone would add, held at the playbook's defaults", im.Pinned)
	list(sWarn, "! %d interzone adds whose defaults couldn't be checked", im.Added)
	for _, wn := range im.Warnings {
		lines = append(lines, "", sWarn.Width(tw).Render("! "+wn))
	}
	lines = append(lines, "", sSub.Width(tw).Render("Nothing on the server changes. The next update writes vars.yml from these answers; Review shows every difference first."),
		"", keyLink("enter")+sMuted.Render(" import it   ")+keyLink("esc")+sMuted.Render(" back"))
	out := strings.Split(strings.Join(lines, "\n"), "\n")
	if len(out) > h {
		out = append(out[:max(1, h-1)], sFaint.Render("…"))
	}
	return strings.Join(out, "\n")
}
