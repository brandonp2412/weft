package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"weft/internal/config"
)

func TestViKeyboard(t *testing.T) {
	base := demoModel(t)
	settings := config.DefaultSettings()
	settings.KeyboardMode = "vi"
	settings.ShowSidebar = false
	settings.HistoryLimit = 20
	settings.HistoryPageSize = 10
	settings.MouseScrollLines = 7
	m := NewWithSettings(context.Background(), nil, base.db, true, settings)
	if m.viInsert || m.input.Focused() || m.sidebarWidth() != 0 || m.limit != 20 {
		t.Fatal("settings not applied")
	}
	press := func(code rune, text string) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = updated.(Model)
	}
	initial := m.current
	press('j', "j")
	if m.current == initial || m.input.Value() != "" {
		t.Fatal("j did not switch chats")
	}
	updated, _ := m.Update(tea.PasteMsg{Content: "ignored"})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatal("paste reached navigation mode")
	}
	press('i', "i")
	press('x', "x")
	if !m.viInsert || m.input.Value() != "x" {
		t.Fatal("insert mode did not compose")
	}
	press(tea.KeyEscape, "")
	press('j', "j")
	if m.viInsert || m.input.Value() != "x" {
		t.Fatal("escape did not restore navigation")
	}
	m.input.Reset()
	press(':', ":")
	if !m.viInsert || m.input.Value() != "/" {
		t.Fatal("command entry failed")
	}
	press(tea.KeyEscape, "")
	press('/', "/")
	if m.mode != "search" {
		t.Fatal("search not opened")
	}
	m.closePrompt()
	if !m.input.Focused() {
		t.Fatal("closing search hid draft cursor")
	}
	m.history()
	if m.limit != 30 {
		t.Fatalf("history limit: %d", m.limit)
	}
}

func TestNormalKeyboardStillComposes(t *testing.T) {
	m := demoModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if updated.(Model).input.Value() != "j" {
		t.Fatal("normal mode intercepted typing")
	}
}

func viModel(t *testing.T) Model {
	base := demoModel(t)
	settings := config.DefaultSettings()
	settings.KeyboardMode = "vi"
	return NewWithSettings(context.Background(), nil, base.db, true, settings)
}
func viKeys(m Model, keys ...string) Model {
	for _, key := range keys {
		if key == "esc" {
			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = updated.(Model)
			continue
		}
		if len([]rune(key)) == 1 {
			updated, _ := m.Update(tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
			m = updated.(Model)
		} else {
			m.updateVi(key)
		}
	}
	return m
}
func TestViDraftNavigation(t *testing.T) {
	m := viModel(t)
	m.input.SetValue("first line\nsecond line\n漢字 third")
	m.viSetPos(3)
	room := m.current
	m = viKeys(m, "j")
	if m.input.Line() != 1 || m.viPos() != 14 || m.current != room {
		t.Fatalf("j: line %d pos %d room %s", m.input.Line(), m.viPos(), m.current)
	}
	m = viKeys(m, "k")
	if m.viPos() != 3 {
		t.Fatalf("k pos %d", m.viPos())
	}
	m = viKeys(m, "0", "w")
	if m.viPos() != 6 {
		t.Fatalf("word pos %d", m.viPos())
	}
	m = viKeys(m, "g", "g", "2", "j", "0")
	if m.input.Line() != 2 || m.viPos() != 23 {
		t.Fatalf("counted motion: %d", m.viPos())
	}
	m = viKeys(m, "l")
	if m.viPos() != 24 {
		t.Fatal("unicode motion")
	}
	if m.input.Value() != "first line\nsecond line\n漢字 third" {
		t.Fatal("motions changed draft")
	}
}
func TestViEditing(t *testing.T) {
	for _, tc := range []struct {
		keys   []string
		want   string
		insert bool
	}{
		{[]string{"d", "w"}, "two\nthree", false},
		{[]string{"d", "d"}, "three", false},
		{[]string{"c", "w"}, "two\nthree", true},
		{[]string{"2", "x"}, "e two\nthree", false},
		{[]string{"A"}, "one two\nthree", true},
		{[]string{"o"}, "one two\n\nthree", true},
		{[]string{"O"}, "\none two\nthree", true},
	} {
		t.Run(strings.Join(tc.keys, ""), func(t *testing.T) {
			m := viModel(t)
			m.input.SetValue("one two\nthree")
			m.viSetPos(0)
			m = viKeys(m, tc.keys...)
			if m.input.Value() != tc.want || m.viInsert != tc.insert {
				t.Fatalf("got %q insert %v", m.input.Value(), m.viInsert)
			}
		})
	}
	m := viModel(t)
	m.input.SetValue("one two")
	m.viSetPos(0)
	m = viKeys(m, "d", "w", "u")
	if m.input.Value() != "one two" {
		t.Fatal("undo failed")
	}
	m = viKeys(m, "ctrl+r")
	if m.input.Value() != "two" {
		t.Fatal("redo failed")
	}
	m = viKeys(m, "P")
	if m.input.Value() != "one two" {
		t.Fatal("register paste failed")
	}
}
func TestViRoomNavigationAndDrafts(t *testing.T) {
	m := viModel(t)
	first := m.current
	m = viKeys(m, "j")
	second := m.current
	if second == first {
		t.Fatal("j did not focus next chat")
	}
	m = viKeys(m, "k")
	if m.current != first {
		t.Fatal("k did not focus previous chat")
	}
	m = viKeys(m, "i", "a", "esc")
	m.nextRoom(1)
	m = viKeys(m, "u")
	if m.input.Value() != "" {
		t.Fatal("undo leaked between rooms")
	}
	m.nextRoom(-1)
	if m.input.Value() != "a" {
		t.Fatal("draft lost")
	}
	m = viKeys(m, ":")
	if m.input.Value() != "a" {
		t.Fatal("command entry destroyed draft")
	}
}

func TestViLineRegistersAndChange(t *testing.T) {
	m := viModel(t)
	m.input.SetValue("one\ntwo\nthree")
	m.viSetPos(0)
	m = viKeys(m, "y", "y", "p")
	if m.input.Value() != "one\none\ntwo\nthree" {
		t.Fatalf("line paste: %q", m.input.Value())
	}
	m = viKeys(m, "g", "g", "c", "c")
	if m.input.Value() != "\none\ntwo\nthree" || !m.viInsert {
		t.Fatalf("line change: %q", m.input.Value())
	}
	m = viKeys(m, "esc", "u")
	if m.input.Value() != "one\none\ntwo\nthree" {
		t.Fatal("undo line change")
	}
}

func TestViWrappedDraft(t *testing.T) {
	m := viModel(t)
	m.input.SetWidth(12)
	m.input.SetValue("a long draft that wraps over several display lines\nlast")
	m.viSetPos(2)
	before := m.viPos()
	room := m.current
	m = viKeys(m, "j")
	if m.viPos() <= before || m.current != room {
		t.Fatal("j did not move in wrapped draft")
	}
	m = viKeys(m, "k")
	if m.viPos() != before {
		t.Fatalf("wrapped k: %d", m.viPos())
	}
}

func TestViEnterStartsInsert(t *testing.T) {
	m := viModel(t)
	m.input.SetValue("draft")
	m.viSetPos(2)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.viInsert || !m.input.Focused() || m.input.Value() != "draft" || m.viPos() != 2 {
		t.Fatal("Enter did not enter insert mode at the cursor")
	}
}

func TestViZZQuits(t *testing.T) {
	m := viModel(t)
	press := func(code rune, text string) tea.Cmd {
		updated, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = updated.(Model)
		return cmd
	}
	if cmd := press('Z', "Z"); cmd != nil {
		t.Fatal("single Z quit")
	}
	cmd := press('Z', "Z")
	if cmd == nil {
		t.Fatal("ZZ did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ZZ did not return QuitMsg")
	}
	m = viModel(t)
	press('Z', "Z")
	press(tea.KeyEscape, "")
	if cmd := press('Z', "Z"); cmd != nil {
		t.Fatal("Escape did not cancel pending Z")
	}
	m = viModel(t)
	press('i', "i")
	press('Z', "Z")
	press('Z', "Z")
	if m.input.Value() != "ZZ" {
		t.Fatal("ZZ intercepted insert mode typing")
	}
}

func TestViQuitConfirmation(t *testing.T) {
	for _, cancel := range []string{"n", "N", "esc"} {
		t.Run(cancel, func(t *testing.T) {
			m := viModel(t)
			m.input.SetValue("draft")
			m.viSetPos(2)
			m = viKeys(m, "q")
			if m.mode != "quit" || !strings.Contains(m.modalView(), "(y/n)") {
				t.Fatal("q did not open exit confirmation")
			}
			m = viKeys(m, "x")
			if m.mode != "quit" {
				t.Fatal("unrelated key dismissed confirmation")
			}
			m = viKeys(m, cancel)
			if m.mode != "" || m.viInsert || m.input.Value() != "draft" || m.viPos() != 2 || !m.input.Focused() {
				t.Fatal("cancel did not restore draft in normal mode")
			}
		})
	}
	for _, yes := range []string{"y", "Y"} {
		m := viKeys(viModel(t), "q")
		_, cmd := m.Update(tea.KeyPressMsg{Code: []rune(yes)[0], Text: yes})
		if cmd == nil {
			t.Fatal("confirmation did not quit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("confirmation did not return QuitMsg")
		}
	}
	m := viKeys(viModel(t), "q", "n")
	if m.input.Focused() {
		t.Fatal("cancel focused empty normal-mode draft")
	}
	m = viKeys(m, "i", "q")
	if m.mode != "" || m.input.Value() != "q" {
		t.Fatal("q intercepted insert mode typing")
	}
	normal := demoModel(t)
	updated, _ := normal.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if updated.(Model).mode != "" || updated.(Model).input.Value() != "q" {
		t.Fatal("q intercepted normal keyboard typing")
	}
}
