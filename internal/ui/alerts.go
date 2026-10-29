package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"interzone/internal/config"
	"interzone/internal/deploy"
)

// ── Alerts ─────────────────────────────────────────────────────────
//
// The server checks itself on a timer and sends a message to ntfy or a
// webhook when something breaks and when it's fixed (deploy/alerts.go).

type alertsScreen struct {
	cfg  *config.Config
	form *form
	err  string
}

func newAlertsScreen() *alertsScreen { return &alertsScreen{} }

func (s *alertsScreen) tool()                {}
func (s *alertsScreen) name() string         { return "Alerts" }
func (s *alertsScreen) title() string        { return "Alerts" }
func (s *alertsScreen) typing() bool         { return s.form.typing() }
func (s *alertsScreen) leave(*config.Config) {}
func (s *alertsScreen) activeForm() *form    { return s.form }
func (s *alertsScreen) keys() [][2]string {
	return [][2]string{{"↑↓", "move"}, {"space", "toggle"}, {"enter", "apply on the server"}}
}

func (s *alertsScreen) enter(c *config.Config) tea.Cmd {
	s.cfg, s.err = c, ""
	a := c.Alerts
	if a.Every == "" {
		a.Every = "*:0/5"
	}
	example := "https://ntfy.sh/a-long-random-topic"
	if c.Components["ntfy"] {
		example = "https://ntfy." + c.Domain + "/a-long-random-topic"
	}
	format := "text"
	if a.JSON {
		format = "json"
	}
	s.form = newForm(
		toggleField("on", "Send alerts", a.Enabled,
			"The server checks itself, even when this computer is off: failed services, the homeserver not answering, a certificate expiring within 14 days, a disk over 90% full."),
		textField("url", "Send them to", a.URL, example,
			"An ntfy topic (subscribe to it in the ntfy app), or a webhook: a Hookshot room webhook, Slack, Discord or Mattermost. Anyone who knows an ntfy topic can read it, so make it long and random."),
		choiceField("format", "Message format", []string{"text", "json"},
			[]string{"Plain text (ntfy)", `JSON {"text": …} (webhooks)`}, format, ""),
		textField("every", "Check", a.Every, "*:0/5", "A systemd calendar time: *:0/5 is every 5 minutes, hourly, daily …"),
	)
	off := func() bool { return !s.form.get("on").on }
	s.form.get("url").hidden, s.form.get("format").hidden, s.form.get("every").hidden = off, off, off
	return nil
}

func (s *alertsScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(formDoneMsg); ok {
		return s.apply()
	}
	if _, ok := msg.(tea.KeyMsg); ok {
		s.err = ""
	}
	return s.form.Update(msg)
}

func (s *alertsScreen) apply() tea.Cmd {
	a := config.Alerts{Enabled: s.form.get("on").on, URL: strings.TrimSpace(s.form.value("url")),
		JSON: s.form.value("format") == "json", Every: strings.TrimSpace(s.form.value("every"))}
	if a.Enabled {
		if p := deploy.AlertsURLProblem(a.URL); p != "" {
			s.err = p
			return nil
		}
	}
	s.cfg.Alerts = a
	_ = s.cfg.Save()
	return run(deploy.Options{Action: deploy.ActionAlerts})
}

func (s *alertsScreen) view(w, h int) string {
	info := sSub.Render("A message is sent when what's wrong changes: once when something breaks, once when it's fixed. Applying sends a test message first.")
	panel := sPanel.Padding(0, 2).Width(min(w, maxFormWidth) - 2).Render(info)
	out := s.form.View(w, h-lipgloss.Height(panel)-3) + "\n\n" + panel
	if s.err != "" {
		out += "\n" + sBad.Render("✗ "+s.err)
	}
	return out
}
