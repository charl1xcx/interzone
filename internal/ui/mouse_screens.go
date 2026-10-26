package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/catalog"
)

// Click handling of each screen (see mouse.go). Lists work the same way
// everywhere: a click opens a row as enter would. Only the Manage actions
// that change the server at once take a second click, so a stray tap
// can't restart it.

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

// listClick handles a click on a listRows row of a screen whose enter acts
// on the cursor row.
func listClick(id string, cursor *int, n int, update func(tea.Msg) tea.Cmd) (tea.Cmd, bool) {
	if _, _, ok := rowClick(id, "row:", cursor, n); !ok {
		return nil, false
	}
	return update(enterKey), true
}

// confirmClick is listClick for a list whose enter asks to confirm: a click
// asks, a second click on the same row confirms, a click on another row
// cancels and selects it.
func confirmClick(id string, cursor *int, confirm *bool, n int, update func(tea.Msg) tea.Cmd) (tea.Cmd, bool) {
	_, again, ok := rowClick(id, "row:", cursor, n)
	switch {
	case !ok:
		return nil, false
	case *confirm && !again:
		*confirm = false
		return nil, true
	}
	return update(enterKey), true
}

// The forms each screen shows.

func (s *serverScreen) activeForm() *form  { return s.form }
func (s *backupsScreen) activeForm() *form { return s.form }
func (s *serversScreen) activeForm() *form {
	if s.imp != nil {
		if s.imp.im != nil {
			return nil
		}
		return s.imp.form
	}
	return s.form
}

func (s *migrateScreen) activeForm() *form {
	if s.confirm {
		return nil
	}
	return s.form
}

func (s *manageScreen) activeForm() *form {
	if s.mode != modeUser {
		return nil
	}
	return s.userForm
}

func (s *usersScreen) activeForm() *form {
	if s.mode != usersLogin && s.mode != usersNew {
		return nil
	}
	return s.form
}

func (s *welcomeScreen) click(id string) (tea.Cmd, bool) {
	if _, _, ok := rowClick(id, "welcome:", &s.choice, 2); ok {
		return s.update(enterKey), true // two big options: one click picks
	}
	return nil, false
}

func (s *componentsScreen) click(id string) (tea.Cmd, bool) {
	if s.details || s.saver != nil {
		return nil, false // the settings form takes it
	}
	items := s.items()
	toggle := func(i int) {
		if it := items[i]; s.available(it) {
			s.selected[it.ID] = !s.selected[it.ID]
		}
	}
	for _, prefix := range []string{"compbox:", "comp:"} {
		if i, _, ok := rowClick(id, prefix, &s.cursor, len(items)); ok {
			toggle(i)
			return nil, true
		}
	}
	if rest, ok := strings.CutPrefix(id, "cat:"); ok {
		i, err := strconv.Atoi(rest)
		if err != nil || i >= len(catalog.Categories) {
			return nil, false
		}
		s.filtering = false
		s.filter.SetValue("")
		s.filter.Blur()
		s.cat, s.cursor, s.offset = i, 0, 0
		return nil, true
	}
	if id == "compfilter" && !s.filtering {
		s.filtering = true
		return s.filter.Focus(), true
	}
	return nil, false
}

func (s *manageScreen) click(id string) (tea.Cmd, bool) {
	switch s.mode {
	case modeList:
		acts := s.actions()
		i, again, ok := rowClick(id, "act:", &s.cursor, len(acts))
		if !ok || acts[i].disabled != "" {
			return nil, ok
		}
		if runsAtOnce[acts[i].id] && !(again && s.armed) {
			s.armed, s.armedKey = true, "" // the row now says "click again to run"
			return nil, true
		}
		return s.choose(acts[i].id), true
	case modeServices:
		if _, _, ok := rowClick(id, "unit:", &s.unit, len(s.units)); ok {
			return s.update(enterKey), true
		}
		return nil, false
	}
	return nil, false
}

func (s *historyScreen) click(id string) (tea.Cmd, bool) {
	return confirmClick(id, &s.cursor, &s.confirm, len(s.list), s.update)
}

func (s *restoreScreen) click(id string) (tea.Cmd, bool) {
	return confirmClick(id, &s.cursor, &s.confirm, len(s.list), s.update)
}

func (s *usersScreen) click(id string) (tea.Cmd, bool) {
	if s.mode != usersList || s.busy {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.rows()), s.update)
}

func (s *serversScreen) click(id string) (tea.Cmd, bool) {
	if s.form != nil || s.confirm || s.imp != nil {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.list), s.update)
}

func (s *tuningScreen) click(id string) (tea.Cmd, bool) {
	if s.editing || s.err != nil || s.fetching {
		return nil, false
	}
	if id == "tfilter" {
		if !s.filtering {
			s.filtering = true
			return s.filter.Focus(), true
		}
		return nil, true
	}
	if s.filtering { // a click in the list ends the search, as ↑↓ do
		s.filtering = false
		s.filter.Blur()
	}
	if s.role < 0 {
		if _, _, ok := rowClick(id, "trole:", &s.roleCursor, len(s.roleItems())); ok {
			return s.roleKey(enterKey), true
		}
		return nil, false
	}
	_, _, ok := rowClick(id, "tvar:", &s.cursor, len(s.items()))
	if ok {
		if v, found := s.current(); found {
			s.startEdit(v)
		}
	}
	return nil, ok
}
