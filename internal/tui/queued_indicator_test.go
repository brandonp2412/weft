package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"maunium.net/go/mautrix/event"
	"weft/internal/store"
)

func TestQueuedIndicatorThreshold(t *testing.T) {
	m := demoModel(t)
	m.width, m.height = 120, 40
	m.settings.QueuedIndicatorDelayMS = 1000
	now := time.UnixMilli(1800000000000)
	m.messages = []store.Message{
		{ID: "previous", Sender: m.userID(), Body: "Previous message", TS: now.UnixMilli()},
		{ID: "pending", Sender: m.userID(), Body: "New message", TS: now.UnixMilli(), Pending: true},
	}
	m.renderTimelineAt(now.Add(999 * time.Millisecond))
	before := append([]string(nil), m.rendered...)
	if strings.Contains(ansi.Strip(strings.Join(before, "\n")), "queued") || !m.nextQueuedAt.Equal(now.Add(1000*time.Millisecond)) {
		t.Fatal("queued indicator appeared before deadline or deadline missing")
	}
	m.renderTimelineAt(now.Add(1000 * time.Millisecond))
	if !strings.Contains(ansi.Strip(strings.Join(m.rendered, "\n")), "queued") || !m.nextQueuedAt.IsZero() || len(before) != len(m.rendered) {
		t.Fatal("indicator missing at deadline or height changed")
	}
	m.messages[1].Pending = false
	m.renderTimelineAt(now.Add(1001 * time.Millisecond))
	if strings.Join(before, "\n") != strings.Join(m.rendered, "\n") {
		t.Fatal("confirmed layout differs from pre-deadline layout")
	}
}

func TestQueuedIndicatorTimerChecksOutbox(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		m := demoModel(t)
		m.settings.QueuedIndicatorDelayMS = 40
		if err := m.db.Enqueue(m.current, "test-txn", event.MessageEventContent{MsgType: event.MsgText, Body: "timer test"}); err != nil {
			t.Fatal(err)
		}
		// Place the enqueue in the future to make scheduling deterministic even on a busy CI host.
		ts := time.Now().Add(250 * time.Millisecond).UnixMilli()
		if _, err := m.db.DB.Exec("UPDATE outbox SET ts=? WHERE txn=?", ts, "test-txn"); err != nil {
			t.Fatal(err)
		}
		m.reload()
		deadline := m.nextQueuedAt
		updated, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m = updated.(Model)
		if cmd == nil || !m.queuedTimerAt.Equal(deadline) || deadline.IsZero() {
			t.Fatal("timer not scheduled")
		}
		if acknowledged {
			if err := m.db.SaveMessageEcho(store.Message{ID: "$ack", RoomID: m.current, Sender: m.userID(), Body: "timer test", TS: ts}, false, "test-txn"); err != nil {
				t.Fatal(err)
			}
		}
		updated, _ = m.Update(queuedIndicatorTimer(deadline)())
		m = updated.(Model)
		for _, msg := range m.messages {
			if msg.Body == "timer test" && msg.Pending == acknowledged {
				t.Fatal("stale pending state after timer")
			}
		}
		if !m.nextQueuedAt.IsZero() {
			t.Fatal("timer did not expire")
		}
		spanID := "test-txn"
		if acknowledged {
			spanID = "$ack"
		}
		span, ok := m.spans[spanID]
		if !ok {
			t.Fatal("message missing")
		}
		if strings.Contains(ansi.Strip(strings.Join(m.rendered[span[0]:span[1]], "\n")), "queued") == acknowledged {
			t.Fatal("incorrect queued state at deadline")
		}
	}
}

func TestSendingIndicatorsDisabledStillShowsFailures(t *testing.T) {
	m := demoModel(t)
	m.settings.ShowSendingIndicators = false
	now := time.Now()
	m.messages = []store.Message{
		{ID: "one", Sender: m.userID(), Body: "First message", TS: now.UnixMilli(), Pending: true},
		{ID: "two", Sender: m.userID(), Body: "Second message", TS: now.UnixMilli(), Pending: true},
	}
	for _, age := range []time.Duration{0, time.Hour} {
		m.renderTimelineAt(now.Add(age))
		if strings.Contains(ansi.Strip(strings.Join(m.rendered, "\n")), "queued") || !m.nextQueuedAt.IsZero() {
			t.Fatal("disabled indicator appeared or scheduled a timer")
		}
	}
	before := len(m.rendered)
	for i := range m.messages {
		m.messages[i].Error = "network error"
	}
	m.renderTimelineAt(now)
	if strings.Count(ansi.Strip(strings.Join(m.rendered, "\n")), "retrying") != 2 || len(m.rendered) != before {
		t.Fatal("failed sends should show a retrying label without changing layout")
	}
}
