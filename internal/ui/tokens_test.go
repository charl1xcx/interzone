package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The screen's commands reach MAS, so these tests only look at what the
// keys set up, not run what they return.
func TestTokensRevokeConfirms(t *testing.T) {
	a := demoApp(t, 120, 36)
	openTokens(a)
	ts := a.cur().(*tokensScreen)
	ts.update(tea.KeyMsg{Type: tea.KeyDown})
	ts.update(runes("x"))
	if !ts.confirm || !strings.Contains(ts.footer(100), "book-club") {
		t.Fatalf("confirm %v: %q", ts.confirm, ts.footer(100))
	}
	// Moving away cancels, as does esc.
	ts.update(tea.KeyMsg{Type: tea.KeyDown})
	if ts.confirm || ts.cursor != 2 {
		t.Fatalf("confirm %v cursor %d", ts.confirm, ts.cursor)
	}
	ts.update(runes("x"))
	ts.update(tea.KeyMsg{Type: tea.KeyEsc})
	if ts.confirm || ts.busy {
		t.Fatal("esc didn't cancel")
	}
	ts.update(runes("x"))
	if cmd := ts.update(runes("y")); cmd == nil || !ts.busy || ts.confirm {
		t.Error("y didn't revoke")
	}
}

func TestTokensFormChecksNumbers(t *testing.T) {
	a := demoApp(t, 120, 36)
	openTokens(a)
	ts := a.cur().(*tokensScreen)
	ts.update(runes("n"))
	if ts.form == nil || len(ts.form.value("token")) != 12 {
		t.Fatal("no form with a token")
	}
	ts.form.get("uses").input.SetValue("two")
	if cmd := ts.create(); cmd != nil || ts.err == nil || ts.form == nil {
		t.Errorf("took uses %q: %v", "two", ts.err)
	}
	ts.form.get("uses").input.SetValue("2")
	ts.form.get("days").input.SetValue("0")
	if cmd := ts.create(); cmd != nil || ts.err == nil {
		t.Errorf("took days 0: %v", ts.err)
	}
}

func TestConfirmKey(t *testing.T) {
	for _, tc := range []struct {
		key    string
		yes    bool
		cursor int
	}{{"y", true, 1}, {"enter", true, 1}, {"n", false, 1}, {"esc", false, 1}, {"down", false, 2}, {"q", false, 1}} {
		cursor, confirm := 1, true
		if yes := confirmKey(tc.key, &cursor, &confirm, 3); yes != tc.yes || confirm || cursor != tc.cursor {
			t.Errorf("%s: yes %v confirm %v cursor %d", tc.key, yes, confirm, cursor)
		}
	}
}
