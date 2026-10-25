package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"interzone/internal/catalog"
)

// Click handling of each screen (see mouse.go). Lists work the same way
// everywhere: a click selects a row, a click on the selected row opens it
// as enter would.

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

// listClick handles a click on a listRows row of a screen whose enter acts
// on the cursor row.
func listClick(id string, cursor *int, n int, update func(tea.Msg) tea.Cmd) (tea.Cmd, bool) {
	_, activate, ok := rowClick(id, "row:", cursor, n)
	if !ok {
		return nil, false
	}
	if activate {
		return update(enterKey), true
	}
	return nil, true
}

// The forms each screen shows.

func (s *networkScreen) activeForm() *form  { return s.form }
func (s *serverScreen) activeForm() *form   { return s.form }
func (s *sslScreen) activeForm() *form      { return s.form }
func (s *settingsScreen) activeForm() *form { return s.form }
func (s *backupsScreen) activeForm() *form  { return s.form }
func (s *serversScreen) activeForm() *form  { return s.form }

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

func (s *homeserverScreen) click(id string) (tea.Cmd, bool) {
	_, activate, ok := rowClick(id, "hs:", &s.sel, len(catalog.Homeservers))
	if ok && activate {
		return nav(1), true
	}
	return nil, ok
}

func (s *componentsScreen) click(id string) (tea.Cmd, bool) {
	items := s.items()
	toggle := func(i int) {
		if it := items[i]; s.available(it) {
			s.selected[it.ID] = !s.selected[it.ID]
		}
	}
	if i, _, ok := rowClick(id, "compbox:", &s.cursor, len(items)); ok {
		toggle(i)
		return nil, true
	}
	if i, activate, ok := rowClick(id, "comp:", &s.cursor, len(items)); ok {
		if activate {
			toggle(i)
		}
		return nil, true
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
		i, activate, ok := rowClick(id, "act:", &s.cursor, len(acts))
		if ok && activate && acts[i].disabled == "" {
			return s.choose(acts[i].id), true
		}
		return nil, ok
	case modeServices:
		_, activate, ok := rowClick(id, "unit:", &s.unit, len(s.units))
		if ok && activate {
			return s.update(enterKey), true
		}
		return nil, ok
	}
	return nil, false
}

func (s *historyScreen) click(id string) (tea.Cmd, bool) {
	if s.confirm {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.list), s.update)
}

func (s *restoreScreen) click(id string) (tea.Cmd, bool) {
	if s.confirm {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.list), s.update)
}

func (s *usersScreen) click(id string) (tea.Cmd, bool) {
	if s.mode != usersList || s.busy {
		return nil, false
	}
	return listClick(id, &s.cursor, len(s.users), s.update)
}

func (s *serversScreen) click(id string) (tea.Cmd, bool) {
	if s.form != nil || s.confirm {
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
		_, activate, ok := rowClick(id, "trole:", &s.roleCursor, len(s.roleItems()))
		if ok && activate {
			return s.roleKey(enterKey), true
		}
		return nil, ok
	}
	_, activate, ok := rowClick(id, "tvar:", &s.cursor, len(s.items()))
	if ok && activate {
		if v, found := s.current(); found {
			s.startEdit(v)
		}
	}
	return nil, ok
}
