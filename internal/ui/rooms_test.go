package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The screen's commands reach Synapse, so these only look at what the keys set up.
func TestRoomsConfirmAndHistory(t *testing.T) {
	a := demoApp(t, 120, 36)
	openRooms(a)
	rs := a.cur().(*roomsScreen)
	rs.update(runes("x")) // deleting can't be undone: the word is typed
	if rs.confirm != "delete" || rs.form == nil || !strings.Contains(rs.view(100, 30), "Delete Lounge?") {
		t.Fatalf("confirm %q: %q", rs.confirm, rs.view(100, 30))
	}
	rs.update(runes("y"))
	if cmd := rs.deleteRoom(); cmd != nil || rs.err == nil || rs.busy {
		t.Error("y deleted the room")
	}
	rs.form.get("sure").input.SetValue("Delete")
	if cmd := rs.deleteRoom(); cmd == nil || rs.form != nil {
		t.Error("typing delete didn't delete")
	}
	rs.busy = false
	rs.update(tea.KeyMsg{Type: tea.KeyDown})
	rs.update(tea.KeyMsg{Type: tea.KeyDown})
	rs.update(tea.KeyMsg{Type: tea.KeyDown})
	rs.update(runes("b"))
	if !strings.Contains(rs.footer(100), "Unblock free crypto?") {
		t.Errorf("blocked room offers: %q", rs.footer(100))
	}
	if cmd := rs.update(runes("y")); cmd == nil || !rs.busy || rs.confirm != "" {
		t.Error("y didn't unblock")
	}
	rs.busy = false
	rs.update(runes("h"))
	rs.form.get("days").input.SetValue("0")
	if cmd := rs.purge(); cmd != nil || rs.err == nil || rs.form == nil {
		t.Errorf("took 0 days: %v", rs.err)
	}
	rs.form.get("days").input.SetValue("30")
	if cmd := rs.purge(); cmd == nil || rs.form != nil {
		t.Error("30 days didn't purge")
	}
}
