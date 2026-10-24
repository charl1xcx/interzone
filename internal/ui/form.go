package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fieldKind int

const (
	fText fieldKind = iota
	fSecret
	fToggle
	fChoice
	fArea
)

type field struct {
	key     string
	label   string
	help    string
	kind    fieldKind
	section string // rendered as a heading above the field when set

	input   textinput.Model
	area    textarea.Model
	on      bool
	choices []string
	labels  []string // display labels for choices; defaults to choices
	choice  int

	required bool
	hidden   func() bool
}

func (f *field) visible() bool { return f.hidden == nil || !f.hidden() }

func (f *field) Value() string {
	switch f.kind {
	case fToggle:
		if f.on {
			return "true"
		}
		return "false"
	case fChoice:
		return f.choices[f.choice]
	case fArea:
		return f.area.Value()
	default:
		return strings.TrimSpace(f.input.Value())
	}
}

func newInput(value, placeholder string, secret bool) textinput.Model {
	in := textinput.New()
	in.SetValue(value)
	in.Placeholder = placeholder
	in.Prompt = ""
	in.CharLimit = 4096
	in.Cursor.Style = lipgloss.NewStyle().Foreground(cPink)
	in.TextStyle = lipgloss.NewStyle().Foreground(cText)
	in.PlaceholderStyle = lipgloss.NewStyle().Foreground(cFaint)
	if secret {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
	}
	return in
}

func textField(key, label, value, placeholder, help string) *field {
	return &field{key: key, label: label, help: help, kind: fText, input: newInput(value, placeholder, false)}
}

func secretField(key, label, value, help string) *field {
	return &field{key: key, label: label, help: help, kind: fSecret, input: newInput(value, "", true)}
}

func toggleField(key, label string, on bool, help string) *field {
	return &field{key: key, label: label, help: help, kind: fToggle, on: on}
}

func choiceField(key, label string, choices, labels []string, value, help string) *field {
	f := &field{key: key, label: label, help: help, kind: fChoice, choices: choices, labels: labels}
	for i, c := range choices {
		if c == value {
			f.choice = i
		}
	}
	return f
}

func areaField(key, label, value, help string) *field {
	ta := textarea.New()
	ta.SetValue(value)
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.CharLimit = 0
	ta.SetHeight(max(3, min(8, strings.Count(value, "\n")+2)))
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(cPurple)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(cFaint)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(cText)
	ta.BlurredStyle.Text = lipgloss.NewStyle().Foreground(cMuted)
	ta.Blur()
	return &field{key: key, label: label, help: help, kind: fArea, area: ta}
}

// form is a vertically scrolling list of fields.
type form struct {
	fields []*field
	focus  int
	reveal bool
	width  int
}

func newForm(fields ...*field) *form {
	f := &form{fields: fields}
	f.focus = -1
	f.move(1)
	return f
}

func (f *form) get(key string) *field {
	for _, fl := range f.fields {
		if fl.key == key {
			return fl
		}
	}
	return nil
}

func (f *form) value(key string) string {
	if fl := f.get(key); fl != nil {
		return fl.Value()
	}
	return ""
}

func (f *form) current() *field {
	if f.focus < 0 || f.focus >= len(f.fields) {
		return nil
	}
	return f.fields[f.focus]
}

// move shifts focus by dir to the next visible field; it reports false when
// it would run off either end.
func (f *form) move(dir int) bool {
	i := f.focus
	for {
		i += dir
		if i < 0 || i >= len(f.fields) {
			return false
		}
		if f.fields[i].visible() {
			break
		}
	}
	if c := f.current(); c != nil {
		c.input.Blur()
		c.area.Blur()
	}
	f.focus = i
	c := f.fields[i]
	switch c.kind {
	case fText, fSecret:
		c.input.Focus()
	case fArea:
		c.area.Focus()
	}
	return true
}

// typing reports whether the focused field consumes printable keys.
func (f *form) typing() bool {
	c := f.current()
	return c != nil && (c.kind == fText || c.kind == fSecret || c.kind == fArea)
}

type formDoneMsg struct{}

// Update handles navigation and editing. Pressing enter on the last field
// returns formDoneMsg so the screen can advance.
func (f *form) Update(msg tea.Msg) tea.Cmd {
	c := f.current()
	if c == nil {
		return nil
	}
	done := func() tea.Msg { return formDoneMsg{} }
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "tab":
			if !f.move(1) {
				return done
			}
			return nil
		case "shift+tab":
			f.move(-1)
			return nil
		case "ctrl+r":
			f.reveal = !f.reveal
			for _, fl := range f.fields {
				if fl.kind == fSecret {
					if f.reveal {
						fl.input.EchoMode = textinput.EchoNormal
					} else {
						fl.input.EchoMode = textinput.EchoPassword
					}
				}
			}
			return nil
		}
		if c.kind == fArea {
			// Enter adds a line inside the box; the arrows leave it once the
			// cursor is on its first or last line.
			switch k.String() {
			case "up":
				if c.area.Line() == 0 {
					f.move(-1)
					return nil
				}
			case "down":
				if c.area.Line() >= c.area.LineCount()-1 {
					if !f.move(1) {
						return done
					}
					return nil
				}
			}
		} else {
			switch k.String() {
			case "up":
				f.move(-1)
				return nil
			case "down", "enter":
				if !f.move(1) && k.String() == "enter" {
					return done
				}
				return nil
			}
		}
		switch c.kind {
		case fToggle:
			if k.String() == " " || k.String() == "x" || k.String() == "left" || k.String() == "right" {
				c.on = !c.on
			}
			return nil
		case fChoice:
			switch k.String() {
			case "left", "h":
				c.choice = (c.choice + len(c.choices) - 1) % len(c.choices)
			case "right", "l", " ":
				c.choice = (c.choice + 1) % len(c.choices)
			}
			return nil
		}
	}
	var cmd tea.Cmd
	switch c.kind {
	case fText, fSecret:
		c.input, cmd = c.input.Update(msg)
	case fArea:
		c.area, cmd = c.area.Update(msg)
	}
	return cmd
}

// View renders the form inside height lines, scrolled so the focused field stays visible.
func (f *form) View(width, height int) string {
	f.width = width
	var lines []string
	focusStart, focusEnd := 0, 0
	for i, fl := range f.fields {
		if !fl.visible() {
			continue
		}
		if fl.section != "" {
			lines = append(lines, strings.Split(sSection.Render("◆ "+fl.section), "\n")...)
		}
		start := len(lines)
		lines = append(lines, strings.Split(f.renderField(fl, i == f.focus, width), "\n")...)
		if i == f.focus {
			focusStart, focusEnd = start, len(lines)
			if fl.section != "" {
				focusStart -= 2 // keep the heading and its margin in view
			}
		}
	}
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	top := 0
	if focusEnd > height {
		top = focusEnd - height
	}
	if focusStart < top {
		top = focusStart
	}
	top = max(0, min(top, len(lines)-height))
	view := lines[top : top+height]
	if top > 0 {
		view[0] = sFaint.Render(strings.Repeat("─", 3) + " ▲ more ")
	}
	if top+height < len(lines) {
		view[len(view)-1] = sFaint.Render(strings.Repeat("─", 3) + " ▼ more ")
	}
	return strings.Join(view, "\n")
}

func (f *form) renderField(fl *field, focused bool, width int) string {
	bar := sFaint.Render("  ")
	label := sMuted.Render(fl.label)
	if focused {
		bar = sAccent.Render("▌ ")
		label = sTitle.Render(fl.label)
	}
	if fl.required {
		label += sPink.Render(" *")
	}
	inner := width - 4
	var body string
	switch fl.kind {
	case fToggle:
		box := sFaint.Render("○")
		if fl.on {
			box = sGood.Render("●")
		}
		return bar + box + " " + label + helpLine(fl, focused, bar)
	case fChoice:
		var parts []string
		for i, c := range fl.choices {
			name := c
			if fl.labels != nil {
				name = fl.labels[i]
			}
			if i == fl.choice {
				parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#111018")).Background(cPurple).Padding(0, 1).Render(name))
			} else {
				parts = append(parts, sMuted.Padding(0, 1).Render(name))
			}
		}
		body = strings.Join(parts, " ")
	case fArea:
		fl.area.SetWidth(max(20, inner))
		body = fl.area.View()
	default:
		fl.input.Width = max(10, inner-2)
		v := fl.input.View()
		if !focused && fl.input.Value() == "" && fl.required {
			v = sBad.Render("required")
		}
		body = v
	}
	out := bar + label + "\n"
	for _, l := range strings.Split(body, "\n") {
		out += bar + "  " + l + "\n"
	}
	return strings.TrimRight(out, "\n") + helpLine(fl, focused, bar)
}

func helpLine(fl *field, focused bool, bar string) string {
	if fl.help == "" || !focused {
		return ""
	}
	return "\n" + bar + "  " + lipgloss.NewStyle().Foreground(cMuted).Italic(true).Render(fl.help)
}
