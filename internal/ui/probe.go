package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Test ───────────────────────────────────────────────────────────
//
// Any Matrix server, as other servers and apps see it from here: its
// well-known files, where federation goes, its signing keys and software,
// its client API and what Element X needs, the network and TLS on the way,
// and the server's sign-ups, rooms and features (deploy.ProbeServer).
// ctrl+o copies the results; ctrl+r tests again every watchEvery.

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

// probeAgainMsg starts the next test while watching.
type probeAgainMsg struct{ gen int }

func (probeAgainMsg) owner() string { return "Test" }

const watchEvery = 30 * time.Second

type probeScreen struct {
	form    *form
	results []probeResult
	gen     int
	spin    spinner.Model
	scroll  int
	err     string
	note    string    // what ctrl+o or ctrl+r did
	watch   bool      // test again every watchEvery
	at      time.Time // when the last test finished
	pb      string    // the playbook, whose pinned versions say what's out of date
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
	stop := "watch"
	if s.watch {
		stop = "stop watching"
	}
	return [][2]string{{"enter", "test"}, {"ctrl+o", "copy"}, {"ctrl+r", stop}, {"↑↓", "scroll"}}
}

func (s *probeScreen) enter(c *config.Config) tea.Cmd {
	if f := s.form.get("servers"); f.input.Value() == "" && c.Domain != "" {
		f.input.SetValue(c.Domain)
	}
	s.pb = config.ExpandHome(c.PlaybookDir)
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
	s.err = ""
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
	if !s.watch {
		s.scroll = 0
	}
	s.gen++
	s.results = nil
	cmds := []tea.Cmd{s.spin.Tick}
	for i, n := range names {
		s.results = append(s.results, probeResult{name: n})
		gen := s.gen
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			return probeMsg{gen, i, deploy.ProbeServer(ctx, n, s.pb)}
		})
	}
	return tea.Batch(cmds...)
}

func (s *probeScreen) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case probeMsg:
		if m.gen != s.gen {
			return nil
		}
		s.results[m.i].findings, s.results[m.i].done = m.findings, true
		if !s.busy() {
			s.at = time.Now()
			if s.watch {
				s.note = "" // so the time it last tested shows
			}
			return s.again()
		}
		return nil
	case probeAgainMsg:
		if m.gen == s.gen && s.watch {
			return s.start()
		}
		return nil
	case spinner.TickMsg:
		return spinning(s.busy(), &s.spin, m)
	case tea.KeyMsg:
		s.err, s.note = "", ""
		switch m.String() {
		case "enter":
			return s.start()
		case "ctrl+o":
			s.copy()
			return nil
		case "ctrl+r":
			s.watch = !s.watch
			switch {
			case !s.watch:
				s.note = "stopped watching"
			case s.results == nil || s.busy():
				s.note = fmt.Sprintf("watching: testing again every %d s", int(watchEvery.Seconds()))
				if s.results == nil {
					return s.start()
				}
			default:
				s.note = fmt.Sprintf("watching: testing again every %d s", int(watchEvery.Seconds()))
				return s.again()
			}
			return nil
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

// again waits to test again, while watching.
func (s *probeScreen) again() tea.Cmd {
	if !s.watch {
		return nil
	}
	gen := s.gen
	return tea.Tick(watchEvery, func(time.Time) tea.Msg { return probeAgainMsg{gen} })
}

// toClipboard is termenv's; tests swap it out.
var toClipboard = termenv.Copy

// copy puts the results on the clipboard, through the terminal (OSC 52,
// which works over SSH too), and in a file for terminals that don't.
func (s *probeScreen) copy() {
	text := s.text()
	if text == "" {
		s.err = "nothing to copy yet: test a server first"
		return
	}
	toClipboard(text)
	path := filepath.Join(config.Dir(), "test-results.txt")
	if os.MkdirAll(config.Dir(), 0o700) == nil && os.WriteFile(path, []byte(text), 0o600) == nil {
		s.note = "copied, and saved to " + tildePath(path)
	} else {
		s.note = "copied"
	}
}

// text is the results as plain text, to paste into a chat.
func (s *probeScreen) text() string {
	var b strings.Builder
	for _, r := range s.results {
		if !r.done {
			continue
		}
		b.WriteString(r.name)
		if !s.at.IsZero() {
			b.WriteString(", tested " + s.at.Format("2006-01-02 15:04"))
		}
		b.WriteString("\n")
		group := ""
		for _, f := range r.findings {
			if f.Group != group {
				group = f.Group
				fmt.Fprintf(&b, "  %s\n", group)
			}
			fmt.Fprintf(&b, "    %s %s: %s\n", [...]string{"✓", "!", "✗"}[f.Level], f.Name, f.Detail)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// tally is the worst level of a tested server, and whether it's done.
func (r probeResult) tally() (deploy.Level, bool) { return deploy.Worst(r.findings), r.done }

func (s *probeScreen) view(w, h int) string {
	top := s.form.View(w, 6)
	switch {
	case s.err != "":
		top += "\n" + sBad.Render("✗ "+s.err)
	case s.note != "":
		top += "\n" + sGood.Render("✓ "+s.note)
	case s.watch && !s.at.IsZero():
		top += "\n" + sMuted.Render(fmt.Sprintf("watching · last tested %s · again every %d s", s.at.Format("15:04:05"), int(watchEvery.Seconds())))
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
			"its names and whether Iran's filter blocks them, where it is, IPv4 and IPv6, how fast it answers, " +
			"its certificates and TLS, /.well-known/matrix/server and /client, SRV records, signing keys, " +
			"the software and whether it's out of date, the client API, ways to log in, what Element X needs " +
			"(sliding sync, OAuth 2.0, a call server), sign-ups, public rooms and Matrix features. " +
			"ctrl+o copies the results; ctrl+r tests again every 30 seconds.")}
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
