package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"weft/internal/store"
)

func TestConsecutiveSendDoesNotMoveTimelineOnAck(t *testing.T) {
	for _, size := range [][2]int{{36, 20}, {80, 24}, {120, 40}} {
		m := demoModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(Model)
		m.settings.QueuedIndicatorDelayMS = 0
		m.messages = nil
		now := time.Now().UnixMilli()
		for i := 0; i < 20; i++ {
			m.messages = append(m.messages, store.Message{ID: fmt.Sprintf("previous-%d", i), Sender: m.userID(), Body: "Previous message", TS: now})
		}
		m.messages = append(m.messages,
			store.Message{ID: "queued1", Sender: m.userID(), Body: "First consecutive send", TS: now, Pending: true},
			store.Message{ID: "queued2", Sender: m.userID(), Body: strings.Repeat("Second consecutive send ", 5), TS: now, Pending: true})
		m.renderTimeline()
		queued := append([]string(nil), m.rendered...)
		start, end := m.visibleTimeline()
		if !strings.Contains(ansi.Strip(strings.Join(queued, "\n")), "queued") {
			t.Fatal("delivery status disappeared")
		}
		m.messages[20].Error = "offline"
		m.renderTimeline()
		if len(m.rendered) != len(queued) || !strings.Contains(ansi.Strip(strings.Join(m.rendered, "\n")), "retrying") {
			t.Fatal("retry changed layout or lost status")
		}
		for i := 20; i < 22; i++ {
			m.messages[i].Pending = false
			m.messages[i].Error = ""
			m.messages[i].ID += "-confirmed"
			m.renderTimeline()
			a, b := m.visibleTimeline()
			if a != start || b != end || len(m.rendered) != len(queued) {
				t.Fatal("acknowledgement moved viewport")
			}
			for row, original := range queued {
				if strings.Contains(ansi.Strip(original), "queued") {
					continue
				}
				if original != m.rendered[row] {
					t.Fatalf("row %d changed on acknowledgement", row)
				}
			}
		}
	}
}
