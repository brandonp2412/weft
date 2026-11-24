package tui

import (
	"strings"
	"testing"
)

func TestPinCommands(t *testing.T) {
	m := demoModel(t)
	room := m.rooms[len(m.rooms)-1].ID
	m.current = room
	m.input.SetValue("draft")
	for range 2 {
		m.command("/pin")
	}
	if m.statusError || m.current != room || m.rooms[0].ID != room || !m.rooms[0].Pinned {
		t.Fatalf("pin failed: %s, %+v", m.status, m.rooms)
	}
	if m.input.Value() != "draft" {
		t.Fatal("pin changed draft")
	}
	if !strings.Contains(m.sidebar(), "*") {
		t.Fatal("sidebar missing pin")
	}
	m.command("/unpin")
	r, err := m.db.Room(room)
	if err != nil || r.Pinned || m.statusError {
		t.Fatalf("unpin failed: %+v %v", r, err)
	}
	m.current = ""
	m.command("/pin")
	if !m.statusError {
		t.Fatal("pin without room should fail")
	}
}

func TestPinCompletion(t *testing.T) {
	m := demoModel(t)
	for _, command := range []string{"pin", "unpin"} {
		m.input.SetValue("/" + command)
		matches := m.commandMatches()
		if len(matches) != 1 || matches[0] != command {
			t.Fatalf("missing completion for %s: %v", command, matches)
		}
	}
}
