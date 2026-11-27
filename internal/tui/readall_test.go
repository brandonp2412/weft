package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"maunium.net/go/mautrix"
	"weft/internal/matrix"
)

func TestReadAllCommand(t *testing.T) {
	for _, reject := range []bool{false, true} {
		m := demoModel(t)
		for _, r := range []string{"!uncached:test", "!invited:test"} {
			membership := "join"
			if r == "!invited:test" {
				membership = "invite"
			}
			if err := m.db.RoomField(r, "membership", membership); err != nil {
				t.Fatal(err)
			}
			if err := m.db.RoomField(r, "unread", 2); err != nil {
				t.Fatal(err)
			}
		}
		m.reload()
		m.demo = false
		m.settings.ReadReceipts = false
		calls := make(chan string, 10)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(r.URL.Path, "/")
			room, endpoint := parts[len(parts)-2], parts[len(parts)-1]
			switch endpoint {
			case "messages":
				if r.Method != "GET" || r.URL.Query().Get("dir") != "b" || r.URL.Query().Get("limit") != "1" {
					t.Errorf("incorrect latest event request: %s", r.URL)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"chunk": []any{map[string]any{"event_id": "$latest" + room, "type": "m.room.message", "content": map[string]any{}}}})
			case "read_markers":
				var markers map[string]string
				if err := json.NewDecoder(r.Body).Decode(&markers); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || markers["m.read"] != "$latest"+room || markers["m.fully_read"] != "$latest"+room {
					t.Errorf("wrong markers: %v", markers)
				}
				calls <- room
				if reject && room == "!matrix:weft.local" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"denied"}`))
					return
				}
				_, _ = w.Write([]byte("{}"))
			default:
				t.Errorf("unexpected endpoint: %s", r.URL)
				w.WriteHeader(404)
			}
		}))
		client, err := mautrix.NewClient(server.URL, "@you:weft.local", "test")
		if err != nil {
			t.Fatal(err)
		}
		m.backend = &matrix.Backend{Client: client, Store: m.db}
		m.input.SetValue("/readall")
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = updated.(Model)
		if m.input.Value() != "/readall " {
			t.Fatal("completion failed")
		}
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		if cmd == nil || !m.busy {
			t.Fatal("command not scheduled")
		}
		updated, _ = m.Update(cmd())
		m = updated.(Model)
		server.Close()
		close(calls)
		seen := map[string]bool{}
		for room := range calls {
			seen[room] = true
		}
		if len(seen) != 3 || !seen["!studio:weft.local"] || !seen["!matrix:weft.local"] || !seen["!uncached:test"] {
			t.Fatalf("unexpected read markers: %v", seen)
		}
		for _, room := range m.rooms {
			want := 0
			if room.ID == "!invited:test" {
				want = 2
			}
			if reject && room.ID == "!matrix:weft.local" {
				want = 12
			}
			if room.Unread != want {
				t.Errorf("%s unread = %d, want %d", room.ID, room.Unread, want)
			}
		}
		if m.busy || m.statusError != reject {
			t.Fatalf("incorrect completion status: %s", m.status)
		}
		if reject && !strings.Contains(m.status, "1 room(s) could not be marked read") {
			t.Fatalf("missing partial failure: %s", m.status)
		}
	}
}

func TestReadAllDemo(t *testing.T) {
	m := demoModel(t)
	m.command("/readall extra")
	if !m.statusError {
		t.Fatal("unexpected argument accepted")
	}
	m.command("/readall")
	for _, room := range m.rooms {
		if room.Unread != 0 {
			t.Fatal("unread room remains")
		}
	}
	if m.statusError || m.status != "All chats marked read" {
		t.Fatal(m.status)
	}
}
