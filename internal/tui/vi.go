package tui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type viSnapshot struct {
	text string
	pos  int
}

func (m Model) viPos() int {
	lines := strings.Split(m.input.Value(), "\n")
	pos := 0
	for i := 0; i < m.input.Line(); i++ {
		pos += len([]rune(lines[i])) + 1
	}
	info := m.input.LineInfo()
	return pos + info.StartColumn + info.ColumnOffset
}
func (m *Model) viSetPos(pos int) {
	text := []rune(m.input.Value())
	pos = min(max(pos, 0), len(text))
	m.input.MoveToBegin()
	row, col := 0, 0
	for _, r := range text[:pos] {
		if r == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	// CursorDown operates on display rows, including wrapped lines.
	for m.input.Line() < row {
		m.input.CursorDown()
	}
	m.input.SetCursorColumn(col)
}
func (m *Model) viSave() {
	m.viUndo = append(m.viUndo, viSnapshot{m.input.Value(), m.viPos()})
	if len(m.viUndo) > 100 {
		m.viUndo = m.viUndo[1:]
	}
	m.viRedo = nil
}
func (m *Model) viRestore(redo bool) {
	from, to := &m.viUndo, &m.viRedo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	*to = append(*to, viSnapshot{m.input.Value(), m.viPos()})
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	m.input.SetValue(s.text)
	m.viSetPos(s.pos)
}
func (m *Model) viMove(key string, count int) {
	r := []rune(m.input.Value())
	p := m.viPos()
	for range count {
		switch key {
		case "h":
			if p > 0 && r[p-1] != '\n' {
				p--
			}
		case "l":
			if p < len(r) && r[p] != '\n' {
				p++
			}
		case "j", "k":
			m.viSetPos(p)
			if key == "j" {
				m.input.CursorDown()
			} else {
				m.input.CursorUp()
			}
			p = m.viPos()
		case "0", "^":
			for p > 0 && r[p-1] != '\n' {
				p--
			}
			if key == "^" {
				for p < len(r) && r[p] != '\n' && unicode.IsSpace(r[p]) {
					p++
				}
			}
		case "$":
			for p < len(r) && r[p] != '\n' {
				p++
			}
		case "w":
			if p < len(r) {
				c := viClass(r[p])
				for p < len(r) && viClass(r[p]) == c {
					p++
				}
				for p < len(r) && unicode.IsSpace(r[p]) {
					p++
				}
			}
		case "b":
			if p > 0 {
				p--
				for p > 0 && unicode.IsSpace(r[p]) {
					p--
				}
				c := viClass(r[p])
				for p > 0 && viClass(r[p-1]) == c {
					p--
				}
			}
		case "e":
			if p < len(r)-1 {
				p++
				for p < len(r)-1 && unicode.IsSpace(r[p]) {
					p++
				}
				c := viClass(r[p])
				for p < len(r)-1 && viClass(r[p+1]) == c {
					p++
				}
			}
		case "gg":
			p = 0
		case "G":
			p = len(r)
		}
	}
	m.viSetPos(p)
}
func viClass(r rune) int {
	if unicode.IsSpace(r) {
		return 0
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return 1
	}
	return 2
}

func (m *Model) viInsertMode() tea.Cmd {
	m.viSave()
	m.viInsert = true
	m.input.Focus()
	return textarea.Blink
}
func (m *Model) updateVi(key string) (tea.Cmd, bool) {
	if key == "esc" {
		m.viCount = 0
		m.viPending = ""
		return nil, false
	}
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || m.viCount > 0) {
		m.viCount = min(1000, m.viCount*10+int(key[0]-'0'))
		return nil, true
	}
	count := max(1, m.viCount)
	m.viCount = 0
	pending := m.viPending
	m.viPending = ""
	if pending == "Z" {
		if key == "Z" {
			return tea.Quit, true
		}
		return nil, true
	}
	if pending == "g" {
		if key == "g" {
			key = "gg"
		} else {
			return nil, true
		}
	}
	if pending == "d" || pending == "c" || pending == "y" {
		if m.input.Value() == "" {
			return nil, true
		}
		start := m.viPos()
		end := start
		m.viLinewise = key == pending
		if key == pending {
			m.viMove("0", 1)
			start = m.viPos()
			m.viMove("j", count-1)
			m.viMove("$", 1)
			end = m.viPos()
			r := []rune(m.input.Value())
			if end < len(r) && pending != "c" {
				end++
			} else if end == len(r) && start > 0 && pending != "c" {
				start--
			}
		} else {
			switch key {
			case "h", "l", "j", "k", "w", "b", "e", "0", "^", "$", "G", "gg":
				m.viMove(key, count)
				end = m.viPos()
				if key == "e" && end < len([]rune(m.input.Value())) {
					end++
				}
			default:
				return nil, true
			}
		}
		if start > end {
			start, end = end, start
		}
		r := []rune(m.input.Value())
		m.viRegister = string(r[start:end])
		if m.viLinewise {
			m.viRegister = strings.Trim(m.viRegister, "\n") + "\n"
		}
		m.viSetPos(start)
		if pending != "y" {
			m.viSave()
			m.input.SetValue(string(r[:start]) + string(r[end:]))
			m.viSetPos(start)
		}
		if pending == "c" {
			m.viInsert = true
			m.input.Focus()
			return textarea.Blink, true
		}
		return nil, true
	}
	if m.input.Value() == "" {
		switch key {
		case "u":
			// Keep undo available after deleting the last text in a draft.
			if !m.input.Focused() || len(m.viUndo) == 0 {
				key = "ctrl+u"
			}
		case "d":
			key = "ctrl+d"
		}
	}
	switch key {
	case "q":
		m.mode = "quit"
		m.input.Blur()
		return nil, true
	case "i", "enter":
		return m.viInsertMode(), true
	case "a":
		m.viMove("l", 1)
		return m.viInsertMode(), true
	case "I":
		m.viMove("^", 1)
		return m.viInsertMode(), true
	case "A":
		m.viMove("$", 1)
		return m.viInsertMode(), true
	case "o", "O":
		m.viSave()
		if key == "o" {
			m.viMove("$", 1)
			m.input.InsertRune('\n')
		} else {
			m.viMove("0", 1)
			p := m.viPos()
			m.input.InsertRune('\n')
			m.viSetPos(p)
		}
		m.viInsert = true
		m.input.Focus()
		return textarea.Blink, true
	case ":":
		if m.input.Value() != "" {
			m.status = "Draft preserved: use / in insert mode for commands"
			return nil, true
		}
		cmd := m.viInsertMode()
		m.input.SetValue("/")
		m.input.CursorEnd()
		return cmd, true
	case "f":
		m.openPrompt("rooms")
		return textinput.Blink, true
	case "/":
		m.openPrompt("search")
		return textinput.Blink, true
	case "H", "M", "L":
		top, end := m.visibleTimeline()
		row := top
		switch key {
		case "M":
			row = top + (end-top)/2
		case "L":
			row = end - 1
		}
		for _, msg := range m.messages {
			span := m.spans[msg.ID]
			if span[0] <= row && row < span[1] {
				m.selected = msg.ID
				m.renderTimeline()
				break
			}
		}
		return nil, true
	case "ctrl+u", "ctrl+d":
		delta := max(1, m.timelineHeight()/2) * count
		if key == "ctrl+u" {
			m.scroll += delta
			m.clampScroll()
			if m.scroll >= max(0, len(m.rendered)-m.timelineHeight()-5) {
				return m.history(), true
			}
		} else {
			m.scroll -= delta
			m.clampScroll()
		}
		return nil, true
	case "u":
		m.viRestore(false)
		return nil, true
	case "ctrl+r":
		m.viRestore(true)
		return nil, true
	case "d", "c", "y", "g", "Z":
		m.viPending = key
		m.viCount = count
		return nil, true
	case "D", "C":
		m.viPending = "d"
		if key == "C" {
			m.viPending = "c"
		}
		return m.updateVi("$")
	case "x":
		r := []rune(m.input.Value())
		p := m.viPos()
		end := p
		for end < len(r) && end < p+count && r[end] != '\n' {
			end++
		}
		if end > p {
			m.viSave()
			m.viLinewise = false
			m.viRegister = string(r[p:end])
			m.input.SetValue(string(r[:p]) + string(r[end:]))
			m.viSetPos(p)
		}
		return nil, true
	case "p", "P":
		if m.viRegister != "" {
			m.viSave()
			if m.viLinewise {
				if key == "p" {
					m.viMove("$", 1)
					pos := m.viPos()
					text := []rune(m.input.Value())
					if pos < len(text) {
						m.viSetPos(pos + 1)
						m.input.InsertString(strings.Repeat(m.viRegister, count))
						m.viSetPos(pos + 1)
					} else {
						m.input.InsertString("\n" + strings.TrimSuffix(strings.Repeat(m.viRegister, count), "\n"))
						m.viSetPos(pos + 1)
					}
				} else {
					m.viMove("0", 1)
					pos := m.viPos()
					m.input.InsertString(strings.Repeat(m.viRegister, count))
					m.viSetPos(pos)
				}
			} else {
				if key == "p" {
					m.viMove("l", 1)
				}
				m.input.InsertString(strings.Repeat(m.viRegister, count))
			}
		}
		return nil, true
	case "j", "k", "h", "l", "w", "b", "e", "0", "^", "$", "gg", "G":
		if m.input.Value() == "" {
			switch key {
			case "j", "k":
				delta := 1
				if key == "k" {
					delta = -1
				}
				for range count {
					m.nextRoom(delta)
				}
				return m.autoRead(), true
			case "gg":
				if len(m.rooms) > 0 {
					m.switchRoom(m.rooms[0].ID)
				}
				return m.autoRead(), true
			case "G":
				if len(m.rooms) > 0 {
					m.switchRoom(m.rooms[len(m.rooms)-1].ID)
				}
				return m.autoRead(), true
			}
		} else {
			m.input.Focus()
			m.viMove(key, count)
		}
		return nil, true
	}
	return nil, false
}
