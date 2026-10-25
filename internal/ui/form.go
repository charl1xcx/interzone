package ui

import (
	"strconv"
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
	id     string // prefix of its click zones
}

var formCount int

func newForm(fields ...*field) *form {
	formCount++
	f := &form{fields: fields, id: "form" + strconv.Itoa(formCount) + ":"}
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

// maxFormWidth keeps inputs and help text at a comfortable reading width.
const maxFormWidth = 96

// View renders the form inside height lines, scrolled so the focused field stays visible.
func (f *form) View(width, height int) string {
	width = min(width, maxFormWidth)
	f.width = width
	var lines []string
	focusStart, focusEnd := 0, 0
	first := true
	for i, fl := range f.fields {
		if !fl.visible() {
			continue
		}
		start := len(lines)
		switch {
		case fl.section != "":
			if !first {
				lines = append(lines, "", "")
				start = len(lines)
			}
			lines = append(lines, sectionHeading(fl.section, width), "")
		case !first:
			lines = append(lines, "")
		}
		first = false
		lines = append(lines, strings.Split(markLines(f.id+"f"+strconv.Itoa(i), f.renderField(fl, i, i == f.focus, width)), "\n")...)
		if i == f.focus {
			focusStart, focusEnd = start, len(lines)
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
		view[0] = mark(f.id+"up", sFaint.Render("  ▲ more above"))
	}
	if top+height < len(lines) {
		view[len(view)-1] = mark(f.id+"down", sFaint.Render("  ▼ more below"))
	}
	return strings.Join(view, "\n")
}

// sectionHeading renders "◆ Title ───────" across the form.
func sectionHeading(title string, width int) string {
	head := sSection.Render("◆ " + title)
	rule := width - lipgloss.Width(head) - 1
	if rule < 3 {
		return head
	}
	return head + " " + sFaint.Render(strings.Repeat("─", rule))
}

func (f *form) renderField(fl *field, idx int, focused bool, width int) string {
	bar := "   "
	label := sMuted.Render(fl.label)
	prompt := sFaint.Render("› ")
	if focused {
		bar = sAccent.Render("▌") + "  "
		label = sTitle.Render(fl.label)
		prompt = sAccent.Render("› ")
	}
	if fl.required {
		label += sPink.Render(" *")
	}
	inner := width - 5
	var body string
	switch fl.kind {
	case fToggle:
		box := sFaint.Render("○")
		if fl.on {
			box = sGood.Render("●")
		}
		state := sFaint.Render("off")
		if fl.on {
			state = sGood.Render("on")
		}
		return bar + box + "  " + label + "  " + state + helpLine(fl, focused, bar, inner)
	case fChoice:
		var parts []string
		for i, c := range fl.choices {
			name := c
			if fl.labels != nil {
				name = fl.labels[i]
			}
			opt := sMuted.Padding(0, 2).Render(name)
			if i == fl.choice {
				opt = lipgloss.NewStyle().Foreground(lipgloss.Color("#111018")).Background(cPurple).Bold(true).Padding(0, 2).Render(name)
			}
			parts = append(parts, mark(f.id+"c"+strconv.Itoa(idx)+"."+strconv.Itoa(i), opt))
		}
		body = strings.Join(parts, "  ")
	case fArea:
		fl.area.SetWidth(max(20, inner))
		body = fl.area.View()
	default:
		fl.input.Width = max(10, inner-3)
		v := fl.input.View()
		if !focused && fl.input.Value() == "" && fl.required {
			v = sBad.Render("required")
		}
		body = prompt + v
	}
	out := bar + label + "\n"
	for _, l := range strings.Split(body, "\n") {
		out += bar + l + "\n"
	}
	return strings.TrimRight(out, "\n") + helpLine(fl, focused, bar, inner)
}

// helpLine wraps the focused field's help text under it.
func helpLine(fl *field, focused bool, bar string, width int) string {
	if fl.help == "" || !focused {
		return ""
	}
	help := lipgloss.NewStyle().Foreground(cMuted).Italic(true).Width(max(20, width)).Render(fl.help)
	return "\n" + bar + strings.ReplaceAll(help, "\n", "\n"+bar)
}

// focusOn moves the focus to field i.
func (f *form) focusOn(i int) {
	if i == f.focus || i < 0 || i >= len(f.fields) || !f.fields[i].visible() {
		return
	}
	f.focus = i - 1
	if !f.move(1) {
		f.focus = i
	}
}

// click handles a click on one of the form's zones: a field takes the
// focus (a toggle also flips), a choice option is picked.
func (f *form) click(id string) (tea.Cmd, bool) {
	rest, ok := strings.CutPrefix(id, f.id)
	if !ok {
		return nil, false
	}
	switch {
	case rest == "up":
		f.move(-1)
	case rest == "down":
		f.move(1)
	case strings.HasPrefix(rest, "c"):
		a, b, _ := strings.Cut(rest[1:], ".")
		i, err1 := strconv.Atoi(a)
		j, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil || i >= len(f.fields) || j >= len(f.fields[i].choices) {
			return nil, false
		}
		f.focusOn(i)
		f.fields[i].choice = j
	case strings.HasPrefix(rest, "f"):
		i, err := strconv.Atoi(rest[1:])
		if err != nil || i >= len(f.fields) {
			return nil, false
		}
		f.focusOn(i)
		if fl := f.fields[i]; fl.kind == fToggle {
			fl.on = !fl.on
		}
	default:
		return nil, false
	}
	return nil, true
}
