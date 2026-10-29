package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Test ───────────────────────────────────────────────────────────
//
// Any Matrix server, as other servers and apps see it from here: its
// well-known files, where federation goes, its signing keys and software,
// its client API and what Element X needs (deploy.ProbeServer).

type probeResult struct {
	name     string
	findings []deploy.Finding
	done     bool
}

type probeMsg struct {
	gen, i   int
	findings []deploy.Finding
}

func (probeMsg) owner() string { return "Test" }

type probeScreen struct {
	form    *form
	results []probeResult
	gen     int
	spin    spinner.Model
	scroll  int
	err     string
}

func newProbeScreen() *probeScreen {
	return &probeScreen{spin: newSpinner(), form: newForm(textField("servers", "Servers to test", "", "matrix.org, example.org",
		"One or more, split by spaces or commas: a server name, a user ID like @alice:example.org, or a URL."))}
}

func (s *probeScreen) name() string         { return "Test" }
func (s *probeScreen) title() string        { return "Test Matrix servers" }
func (s *probeScreen) typing() bool         { return true }
func (s *probeScreen) leave(*config.Config) {}
func (s *probeScreen) activeForm() *form    { return s.form }
func (s *probeScreen) keys() [][2]string {
	return [][2]string{{"enter", "test"}, {"↑↓", "scroll"}}
}

func (s *probeScreen) enter(c *config.Config) tea.Cmd {
	if f := s.form.get("servers"); f.input.Value() == "" && c.Domain != "" {
		f.input.SetValue(c.Domain)
	}
	return nil
}

func (s *probeScreen) busy() bool {
	for _, r := range s.results {
		if !r.done {
			return true
		}
	}
	return false
}

// start tests each server named, all at once.
func (s *probeScreen) start() tea.Cmd {
	s.err, s.scroll = "", 0
	seen := map[string]bool{}
	var names []string
	for _, w := range strings.FieldsFunc(s.form.value("servers"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if n := deploy.ServerName(w); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		s.err = "type a server name first, e.g. matrix.org"
		return nil
	}
	s.gen++
	s.results = nil
	cmds := []tea.Cmd{s.spin.Tick}
	for i, n := range names {
		s.results = append(s.results, probeResult{name: n})
		gen := s.gen
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			return probeMsg{gen, i, deploy.ProbeServer(ctx, n)}
		})
	}
	return tea.Batch(cmds...)
}

func (s *probeScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case probeMsg:
		if m.gen == s.gen {
			s.results[m.i].findings, s.results[m.i].done = m.findings, true
		}
		return nil
	case spinner.TickMsg:
		return spinning(s.busy(), &s.spin, m)
	case tea.KeyMsg:
		s.err = ""
		switch m.String() {
		case "enter":
			return s.start()
		case "up":
			s.scroll = max(0, s.scroll-1)
			return nil
		case "down":
			s.scroll++
			return nil
		}
	}
	return s.form.Update(msg)
}

// tally is the worst level of a tested server, and whether it's done.
func (r probeResult) tally() (deploy.Level, bool) { return deploy.Worst(r.findings), r.done }

func (s *probeScreen) view(w, h int) string {
	top := s.form.View(w, 6)
	if s.err != "" {
		top += "\n" + sBad.Render("✗ "+s.err)
	}
	var rows []string
	for _, r := range s.results {
		head := sTitle.Render(r.name)
		switch {
		case !r.done:
			head = s.spin.View() + " " + head + sMuted.Render("  testing…")
		default:
			fails, warns := 0, 0
			for _, f := range r.findings {
				switch f.Level {
				case deploy.Fail:
					fails++
				case deploy.Warn:
					warns++
				}
			}
			icon, note := sGood.Render("✓"), sGood.Render("  all good")
			switch {
			case fails > 0:
				icon, note = sBad.Render("✗"), sBad.Render(fmt.Sprintf("  %d failed", fails))
				if warns > 0 {
					note += sWarn.Render(", " + plural(warns, "warning"))
				}
			case warns > 0:
				icon, note = sWarn.Render("!"), sWarn.Render("  "+plural(warns, "warning"))
			}
			head = icon + " " + head + note
		}
		rows = append(rows, "", head)
		group := ""
		for _, f := range r.findings {
			if f.Group != group {
				group = f.Group
				rows = append(rows, "  "+sSection.Render(group))
			}
			name := lipgloss.NewStyle().Width(18).Render(sTitle.Render(f.Name))
			if w < 60 {
				name = sTitle.Render(f.Name)
			}
			rows = append(rows, strings.Split(findingRow(f, name, w, true), "\n")...)
		}
	}
	if len(rows) == 0 {
		rows = []string{"", sSub.Render("Each server is looked at from this computer, as other servers and apps see it: " +
			"/.well-known/matrix/server and /client, SRV records, signing keys, the software and its version, " +
			"the client API, ways to log in, and what Element X needs (sliding sync, OAuth 2.0, a call server).")}
		rows = strings.Split(lipgloss.NewStyle().Width(min(w, maxFormWidth)).Render(strings.Join(rows, "\n")), "\n")
	}
	room := max(3, h-lipgloss.Height(top)-1)
	s.scroll = min(s.scroll, max(0, len(rows)-room))
	end := min(len(rows), s.scroll+room)
	shown := append([]string(nil), rows[s.scroll:end]...)
	if end < len(rows) {
		shown[len(shown)-1] = sFaint.Render(fmt.Sprintf("  ↓ %d more · ↑↓ scroll", len(rows)-end))
	}
	return top + "\n" + strings.Join(shown, "\n")
}
