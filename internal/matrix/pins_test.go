package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"maunium.net/go/mautrix"
	"weft/internal/store"
)

func TestSyncRoomFavourites(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client, _ := mautrix.NewClient("https://example.org", "@me:test", "")
	b := &Backend{ctx: context.Background(), Client: client, Store: db, synced: make(chan struct{})}
	ds := mautrix.NewDefaultSyncer()
	ds.OnEvent(b.handleEvent)
	s := &syncer{DefaultSyncer: ds, b: b}
	process := func(tags string, since string) {
		t.Helper()
		var res mautrix.RespSync
		if err := json.Unmarshal([]byte(`{"next_batch":"next","rooms":{"join":{"!tyla:test":{"account_data":{"events":`+tags+`}},"!recent:test":{}}}}`), &res); err != nil {
			t.Fatal(err)
		}
		if err := s.ProcessResponse(context.Background(), &res, since); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsureRoom("!recent:test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMessage(store.Message{ID: "$new", RoomID: "!recent:test", Sender: "@a:test", Kind: "m.text", TS: 100}, false); err != nil {
		t.Fatal(err)
	}
	process(`[{"type":"m.tag","content":{"tags":{"m.favourite":{"order":0.1},"u.other":{}}}}]`, "")
	rooms, err := db.Rooms()
	if err != nil || rooms[0].ID != "!tyla:test" || !rooms[0].Pinned {
		t.Fatalf("favourite not first: %+v %v", rooms, err)
	}
	process(`[]`, "next")
	room, _ := db.Room("!tyla:test")
	if !room.Pinned {
		t.Fatal("missing account data cleared favourite")
	}
	process(`[{"type":"m.tag","content":{"tags":{"u.other":{}}}}]`, "next")
	rooms, err = db.Rooms()
	if err != nil || rooms[0].ID != "!recent:test" || rooms[1].Pinned {
		t.Fatalf("unfavourite not applied: %+v %v", rooms, err)
	}
	process(`[{"type":"m.tag","content":{"tags":{"m.favourite":{},"m.lowpriority":{}}}}]`, "next")
	rooms, err = db.Rooms()
	if err != nil || len(rooms) != 2 || rooms[1].ID != "!tyla:test" || !rooms[1].Pinned {
		t.Fatalf("low-priority favourite should sort last: %+v %v", rooms, err)
	}
	process(`[{"type":"m.tag","content":{"tags":{"m.favourite":{}}}}]`, "next")
	rooms, err = db.Rooms()
	if err != nil || len(rooms) != 2 || rooms[0].ID != "!tyla:test" {
		t.Fatalf("removing low-priority tag should restore ordering: %+v %v", rooms, err)
	}
}

func TestPinRoomUsesMatrixTagEndpoint(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/user/@me:test/rooms/!tyla:test/tags/m.favourite" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		if fail {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"denied"}`))
			return
		}
		if r.Method != http.MethodPut && r.Method != http.MethodDelete {
			t.Errorf("wrong method: %s", r.Method)
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureRoom("!tyla:test"); err != nil {
		t.Fatal(err)
	}
	client, _ := mautrix.NewClient(server.URL, "@me:test", "token")
	b := &Backend{Client: client, Store: db}
	for _, pinned := range []bool{true, false} {
		if err := b.PinRoom(context.Background(), "!tyla:test", pinned); err != nil {
			t.Fatal(err)
		}
		room, _ := db.Room("!tyla:test")
		if room.Pinned != pinned {
			t.Fatalf("cache incorrect: %+v", room)
		}
	}
	fail = true
	if err := b.PinRoom(context.Background(), "!tyla:test", true); err == nil {
		t.Fatal("server failure ignored")
	}
	room, _ := db.Room("!tyla:test")
	if room.Pinned {
		t.Fatal("failed request changed cache")
	}
}

func TestStartupTagsDoNotWaitForTimelineOrReplaceSyncToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/sync" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		var filter struct {
			Room struct {
				AccountData struct {
					Types []string `json:"types"`
				} `json:"account_data"`
				Timeline struct {
					NotTypes []string `json:"not_types"`
				} `json:"timeline"`
			} `json:"room"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filter")), &filter); err != nil {
			t.Error(err)
		}
		if len(filter.Room.AccountData.Types) != 1 || filter.Room.AccountData.Types[0] != "m.tag" {
			t.Error("tags not requested")
		}
		if len(filter.Room.Timeline.NotTypes) != 1 || filter.Room.Timeline.NotTypes[0] != "*" {
			t.Error("timeline not excluded")
		}
		if r.URL.Query().Get("since") != "" {
			t.Error("startup must load existing tags")
		}
		_, _ = w.Write([]byte(`{"next_batch":"tags-only-token","rooms":{"join":{"!tyla:test":{"account_data":{"events":[{"type":"m.tag","content":{"tags":{"m.favourite":{}}}}]}},"!old:test":{"account_data":{"events":[]}}}}}`))
	}))
	defer server.Close()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Put("next_batch", "timeline-token"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureRoom("!old:test"); err != nil {
		t.Fatal(err)
	}
	if err := db.PinRoom("!old:test", true); err != nil {
		t.Fatal(err)
	}
	client, _ := mautrix.NewClient(server.URL, "@me:test", "token")
	b := &Backend{Client: client, Store: db}
	if err := b.SyncRoomTags(context.Background()); err != nil {
		t.Fatal(err)
	}
	rooms, err := db.Rooms()
	if err != nil || len(rooms) != 2 || rooms[0].ID != "!tyla:test" || !rooms[0].Pinned || rooms[1].Pinned {
		t.Fatalf("startup tags: %+v %v", rooms, err)
	}
	if db.Get("next_batch") != "timeline-token" {
		t.Fatal("tag bootstrap replaced timeline token")
	}
}
