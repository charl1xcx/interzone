package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/deploy"
)

// ── Health ─────────────────────────────────────────────────────────
//
// Once a server is installed the header shows whether it answers over
// HTTPS and how long its certificate has left, checked every minute from
// this machine (no SSH involved). INTERZONE_HEALTH=off turns it off.

const healthEvery = time.Minute

type healthMsg struct {
	gen int
	h   *deploy.Health // nil: not installed, nothing was checked
}

type healthTickMsg struct{ gen int }

// checkHealth starts a check for the current server; results of checks
// started before the last switch are dropped by gen.
func (a *App) checkHealth() tea.Cmd {
	gen := a.healthGen
	if !a.cfg.Installed() || os.Getenv("INTERZONE_HEALTH") == "off" {
		return func() tea.Msg { return healthMsg{gen: gen} }
	}
	host := a.cfg.HostName()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		h := deploy.CheckHealth(ctx, host)
		return healthMsg{gen, &h}
	}
}

// restartHealth forgets the last result and checks again now.
func (a *App) restartHealth() tea.Cmd {
	a.healthGen++
	a.health = nil
	return a.checkHealth()
}

func (a *App) onHealth(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case healthMsg:
		if m.gen != a.healthGen {
			return nil
		}
		a.health = m.h
		return tea.Tick(healthEvery, func(time.Time) tea.Msg { return healthTickMsg{m.gen} })
	case healthTickMsg:
		if m.gen == a.healthGen {
			return a.checkHealth()
		}
	}
	return nil
}

// healthBadge is "● up · cert 63d" and the like; short drops the words.
func (a *App) healthBadge(short bool) string {
	h := a.health
	if h == nil {
		return ""
	}
	if !h.Up {
		if short {
			return sBad.Render("● down")
		}
		return sBad.Render("● not answering") + sFaint.Render(" · "+truncate(h.Err, 32))
	}
	out := sGood.Render("●")
	if !short {
		out += sMuted.Render(" up")
	}
	if h.CertLeft != 0 {
		days := int(h.CertLeft.Hours() / 24)
		style := sMuted
		switch {
		case days < 7:
			style = sBad
		case days < 21:
			style = sWarn
		}
		label := fmt.Sprintf("cert %dd", days)
		switch {
		case days < 0:
			label = "cert expired"
		case short:
			label = fmt.Sprintf("%dd", days)
		}
		if !h.Trusted && !short {
			label += " (untrusted)"
		}
		out += sFaint.Render(" · ") + style.Render(label)
	}
	return out
}
