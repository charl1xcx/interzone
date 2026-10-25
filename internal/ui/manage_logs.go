package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Live logs ──────────────────────────────────────────────────────
//
// Manage › View logs follows a service's journal as it is written, until
// esc. Lines arrive in batches so a chatty service doesn't redraw the
// screen for every one.

type logLinesMsg struct {
	gen   int // which stream they're from; older streams' lines are dropped
	lines []string
	open  bool // false: the stream has ended
}

type liveLogs struct {
	unit    string
	gen     int
	ch      <-chan string
	cancel  context.CancelFunc
	lines   []string
	styled  []string // lines as drawn, styledW wide
	styledW int
	vp      viewport.Model
	follow  bool
	ended   bool
}

// start follows unit's journal on c's server.
func (l *liveLogs) start(c *config.Config, unit string) tea.Cmd {
	l.stop()
	l.gen++
	l.unit, l.lines, l.styled, l.follow, l.ended = unit, nil, nil, true, false
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := deploy.FollowLogs(ctx, c, unit)
	if err != nil {
		cancel()
		l.lines, l.ended = []string{"✗ " + err.Error()}, true
		l.styledW = 0
		return nil
	}
	l.ch, l.cancel = ch, cancel
	return l.wait()
}

func (l *liveLogs) stop() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// wait reads the next lines: blocks for one, then takes whatever else is
// already there.
func (l *liveLogs) wait() tea.Cmd {
	ch, gen := l.ch, l.gen
	return func() tea.Msg {
		first, ok := <-ch
		if !ok {
			return logLinesMsg{gen: gen}
		}
		lines := []string{first}
		for len(lines) < 500 {
			select {
			case line, ok := <-ch:
				if !ok {
					return logLinesMsg{gen, lines, false}
				}
				lines = append(lines, line)
			default:
				return logLinesMsg{gen, lines, true}
			}
		}
		return logLinesMsg{gen, lines, true}
	}
}

func (l *liveLogs) add(m logLinesMsg) tea.Cmd {
	if m.gen != l.gen {
		return nil
	}
	l.lines = append(l.lines, m.lines...)
	if l.styledW > 0 { // not drawn yet: view styles them all
		for _, line := range m.lines {
			l.styled = append(l.styled, logLine(line, l.styledW))
		}
	}
	if len(l.lines) > 5000 {
		cut := len(l.lines) - 4000
		l.lines = l.lines[cut:]
		l.styled = l.styled[min(cut, len(l.styled)):]
	}
	if !m.open {
		l.ended = true
		l.stop()
		return nil
	}
	return l.wait()
}

func (l *liveLogs) key(k string) {
	switch k {
	case "end", "G":
		l.follow = true
		l.vp.GotoBottom()
	case "up", "k", "pgup", "home", "g":
		l.follow = false
	}
	if m, ok := keyMsg(k); ok {
		l.vp, _ = l.vp.Update(m)
	}
	if l.vp.AtBottom() {
		l.follow = true
	}
}

func (l *liveLogs) view(w, h int) string {
	state := sGood.Render("● following")
	switch {
	case l.ended:
		state = sFaint.Render("■ stream ended")
	case !l.follow:
		state = sWarn.Render("❚❚ paused") + sFaint.Render(" · end follows again")
	}
	head := sTitle.Render(l.unit) + "  " + state
	l.vp.Width, l.vp.Height = w, max(3, h-2)
	if w != l.styledW || len(l.styled) != len(l.lines) {
		l.styledW = w
		l.styled = make([]string, len(l.lines))
		for i, line := range l.lines {
			l.styled[i] = logLine(line, w)
		}
	}
	l.vp.SetContent(strings.Join(l.styled, "\n"))
	if l.follow {
		l.vp.GotoBottom()
	}
	return head + "\n\n" + l.vp.View()
}

// logLine dims a journal line's timestamp and host, and fits it in w.
func logLine(line string, w int) string {
	if strings.HasPrefix(line, "✗ ") {
		return sBad.Render(truncate(line, w))
	}
	line = truncate(line, w)
	if prefix, msg, ok := strings.Cut(line, ": "); ok && strings.Count(prefix, " ") <= 3 {
		return sFaint.Render(prefix+":") + " " + msg
	}
	return line
}
