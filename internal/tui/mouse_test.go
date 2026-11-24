package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSidebarClickSwitchesRoomAndPreservesDraft(t *testing.T) {
	m := demoModel(t)
	m.current = m.rooms[1].ID
	previous := m.current
	target := m.rooms[0].ID
	m.input.SetValue("unfinished message")
	m.drafts[target] = "target draft"
	m.scroll = 5
	updated, _ := m.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	m = updated.(Model)
	if m.current != target || m.scroll != 0 || m.input.Value() != "target draft" || m.drafts[previous] != "unfinished message" {
		t.Fatalf("sidebar click did not switch room with drafts preserved: current=%q scroll=%d draft=%q", m.current, m.scroll, m.input.Value())
	}
}

func TestSidebarClickUsesVisibleRoomRange(t *testing.T) {
	m := demoModel(t)
	m.height = 14
	// Repeat room entries to exercise the sidebar viewport with a long list.
	rooms := append(m.rooms[:len(m.rooms):len(m.rooms)], m.rooms...)
	m.rooms = rooms
	m.current = rooms[len(rooms)-1].ID
	start, _ := m.sidebarRoomRange()
	if start == 0 {
		t.Fatal("test needs a scrolled sidebar")
	}
	target := rooms[start].ID
	updated, _ := m.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	if updated.(Model).current != target {
		t.Fatal("click did not select the visible first room")
	}
}

func TestSidebarClickIgnoresOtherAreas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x, y   int
		button tea.MouseButton
		mode   string
		hidden bool
	}{
		{"header", 3, 3, tea.MouseLeft, "", false},
		{"border", 24, 4, tea.MouseLeft, "", false},
		{"timeline", 35, 4, tea.MouseLeft, "", false},
		{"footer", 3, 29, tea.MouseLeft, "", false},
		{"right click", 3, 4, tea.MouseRight, "", false},
		{"modal", 3, 4, tea.MouseLeft, "rooms", false},
		{"hidden sidebar", 3, 4, tea.MouseLeft, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := demoModel(t)
			m.current = m.rooms[1].ID
			m.mode = tc.mode
			m.settings.ShowSidebar = !tc.hidden
			updated, _ := m.Update(tea.MouseClickMsg{X: tc.x, Y: tc.y, Button: tc.button})
			if updated.(Model).current != m.current {
				t.Fatal("click unexpectedly switched rooms")
			}
		})
	}
}
