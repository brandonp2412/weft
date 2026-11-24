package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"maunium.net/go/mautrix"
	"weft/internal/matrix"
	"weft/internal/store"
)

func demoModel(t *testing.T) Model {
	t.Helper()
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = SeedDemo(db); e != nil {
		t.Fatal(e)
	}
	return New(context.Background(), nil, db, true)
}
func TestOpeningThreadSchedulesReadReceipt(t *testing.T) {
	m := demoModel(t)
	m.demo = false
	m.backend = &matrix.Backend{Client: &mautrix.Client{UserID: "@you:weft.local"}}
	m.switchRoom("!studio:weft.local")

	if cmd := m.command("/thread $demo-1"); cmd == nil || m.lastRead[m.current] != "$demo-1" {
		t.Fatal("opening a thread should schedule a read receipt for its latest event")
	}
}

func TestLayoutFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{36, 14}, {60, 20}, {80, 24}, {120, 40}, {180, 50}} {
		for _, mode := range []string{"", "help", "rooms", "details", "recover", "search-results"} {
			m := demoModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = updated.(Model)
			m.mode = mode
			m.details = strings.Repeat("Long details and Unicode 🌿 🧶 漢字\n", 100)
			view := m.View().Content
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Errorf("%v mode %q: height %d exceeds %d", size, mode, len(lines), size[1])
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%v mode %q line %d: width %d exceeds %d: %q", size, mode, i, w, size[0], ansi.Strip(l))
				}
			}
		}
	}
}
func TestSecretPasteDoesNotReachComposer(t *testing.T) {
	m := demoModel(t)
	m.input.SetValue("draft")
	m.openPrompt("recover")
	updated, _ := m.Update(tea.PasteMsg{Content: "super secret recovery key"})
	m = updated.(Model)
	if m.input.Value() != "draft" {
		t.Fatal("secret paste reached the composer")
	}
	if m.prompt.Value() != "super secret recovery key" {
		t.Fatal("secret paste was lost")
	}
	if strings.Contains(m.View().Content, "super secret") {
		t.Fatal("secret visible in UI")
	}
	m.closePrompt()
	if m.prompt.Value() != "" {
		t.Fatal("secret retained after closing prompt")
	}
}
func TestTerminalControlSequencesAreNotRendered(t *testing.T) {
	m := demoModel(t)
	evil := "text\x1b]52;c;YXR0YWNr\x07\x1b[31m\x1b[2J"
	msg := store.Message{ID: "$evil", RoomID: m.current, Sender: "@evil:x", Body: evil, TS: 9999999999999, Kind: "m.text"}
	if e := m.db.SaveMessage(msg, false); e != nil {
		t.Fatal(e)
	}
	_ = m.db.SetMember(m.current, msg.Sender, evil)
	m.reload()
	view := m.View().Content
	for _, sequence := range []string{"\x1b]52", "\x1b[31m", "\x1b[2J"} {
		if strings.Contains(view, sequence) {
			t.Fatalf("untrusted terminal sequence rendered: %q", sequence)
		}
	}
}
func TestRoomDraftsAndHistoryAnchor(t *testing.T) {
	m := demoModel(t)
	room := m.current
	m.input.SetValue("unfinished")
	m.nextRoom(1)
	if m.input.Value() != "" {
		t.Fatal("draft leaked across rooms")
	}
	m.switchRoom(room)
	if m.input.Value() != "unfinished" {
		t.Fatal("draft lost")
	}
	m.scroll = 5
	m.clampScroll()
	oldTop := len(m.rendered) - m.timelineHeight() - m.scroll
	oldLines := append([]string(nil), m.rendered...)
	_ = m.db.SaveMessage(store.Message{ID: "$new", RoomID: room, Sender: m.userID(), Body: "new message", Kind: "m.text", TS: 9999999999999}, false)
	m.reload()
	newTop := len(m.rendered) - m.timelineHeight() - m.scroll
	if oldTop >= 0 && newTop >= 0 && oldLines[oldTop] != m.rendered[newTop] {
		t.Fatal("incoming message moved the scrolled viewport")
	}
}
func TestHelpScrollsAndQueuedTargetsRejected(t *testing.T) {
	m := demoModel(t)
	m.mode = "help"
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = updated.(Model)
	if m.promptIndex == 0 {
		t.Fatal("help did not scroll")
	}
	m.messages[0].Pending = true
	if _, e := m.target("#1"); e == nil {
		t.Fatal("queued message accepted as server event target")
	}
}

func TestLeavingRoomSelectsRemainingConversation(t *testing.T) {
	m := demoModel(t)
	left := m.current
	if e := m.db.RoomField(left, "membership", "leave"); e != nil {
		t.Fatal(e)
	}
	m.reload()
	if m.current == left || m.current == "" {
		t.Fatal("left room remained selected")
	}
	m.nextRoom(1)
	saved := m.current
	restarted := New(context.Background(), nil, m.db, true)
	if restarted.current != saved {
		t.Fatal("selected conversation not restored")
	}
}

func TestMessageTimeFormat(t *testing.T) {
	m := demoModel(t)
	for _, tc := range []struct{ format, want string }{
		{"24h", "00:05"}, {"12h", "12:05 AM"},
	} {
		m.settings.TimeFormat = tc.format
		m.messages = []store.Message{{ID: "$clock", Sender: "@clock:test", Body: "hello", TS: time.Date(2026, 11, 24, 0, 5, 0, 0, time.Local).UnixMilli()}}
		m.renderTimeline()
		timeline := ansi.Strip(strings.Join(m.rendered, "\n"))
		if !strings.Contains(timeline, "@clock:test  "+tc.want) || strings.Contains(timeline, "#1") {
			t.Fatalf("%s timeline: %s", tc.format, timeline)
		}
		m.mode = "search-results"
		m.search = m.messages
		if view := ansi.Strip(m.modalView()); !strings.Contains(view, "Nov 24 "+tc.want) {
			t.Fatalf("%s search: %s", tc.format, view)
		}
	}
}

func TestRoomPickerStaysStationaryWhileTyping(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {120, 40}} {
		m := demoModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(Model)
		m.openPrompt("rooms")
		row := func() int {
			for i, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if strings.Contains(line, "Switch conversation") {
					return i
				}
			}
			t.Fatal("missing picker title")
			return -1
		}
		initial := row()
		for _, query := range []string{m.rooms[0].Title(), "no matching conversation", strings.Repeat("long query ", 30), ""} {
			m.prompt.SetValue(query)
			m.prompt.CursorEnd()
			if got := row(); got != initial {
				t.Fatalf("%v query %q: picker moved from row %d to %d", size, query, initial, got)
			}
			for _, line := range strings.Split(m.View().Content, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal("long query overflowed terminal")
				}
			}
		}
	}
}

func TestRoomPickerSelectionTracksQueryChanges(t *testing.T) {
	m := demoModel(t)
	m.openPrompt("rooms")
	press := func(code rune) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: code})
		m = updated.(Model)
	}
	for range len(m.rooms) + 5 {
		press(tea.KeyDown)
	}
	if m.promptIndex != len(m.rooms)-1 {
		t.Fatal("selection moved beyond results")
	}
	press(tea.KeyLeft)
	if m.promptIndex != len(m.rooms)-1 {
		t.Fatal("cursor movement reset selection")
	}
	updated, _ := m.Update(tea.PasteMsg{Content: m.rooms[0].Title()})
	m = updated.(Model)
	if m.promptIndex != 0 {
		t.Fatal("pasted query did not reset selection")
	}
}
