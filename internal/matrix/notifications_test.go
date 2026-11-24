package matrix

import (
	"context"
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"weft/internal/store"
)

func TestSyncNotifications(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client, err := mautrix.NewClient("https://example.org", "@me:test", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &Backend{ctx: context.Background(), Client: client, Store: db, synced: make(chan struct{}), Notifications: make(chan store.Message, 10)}
	ds := mautrix.NewDefaultSyncer()
	ds.OnEvent(b.handleEvent)
	s := &syncer{DefaultSyncer: ds, b: b}
	process := func(events string, accountData ...string) {
		t.Helper()
		var res mautrix.RespSync
		roomData, globalData := "[]", "[]"
		if len(accountData) > 0 {
			roomData, globalData = accountData[0], accountData[1]
		}
		err := json.Unmarshal([]byte(`{"next_batch":"next","account_data":{"events":`+globalData+`},"rooms":{"join":{"!room:test":{"account_data":{"events":`+roomData+`},"timeline":{"events":`+events+`}}}}}`), &res)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ProcessResponse(context.Background(), &res, "previous"); err != nil {
			t.Fatal(err)
		}
	}
	msg := func(id, sender, content string) string {
		return `{"event_id":"` + id + `","sender":"` + sender + `","type":"m.room.message","origin_server_ts":123,"content":` + content + `}`
	}
	old := msg("$old", "@other:test", `{"msgtype":"m.text","body":"old"}`)
	process("[" + old + "]")
	if len(b.Notifications) != 0 {
		t.Fatal("startup notification")
	}
	fresh := msg("$new", "@other:test", `{"msgtype":"m.text","body":"new"}`)
	own := msg("$own", "@me:test", `{"msgtype":"m.text","body":"own"}`)
	edit := msg("$edit", "@other:test", `{"msgtype":"m.text","body":"* edit","m.relates_to":{"rel_type":"m.replace","event_id":"$new"},"m.new_content":{"msgtype":"m.text","body":"edit"}}`)
	process("[" + old + "," + fresh + "," + own + "," + edit + "]")
	if len(b.Notifications) != 1 {
		t.Fatalf("got %d alerts", len(b.Notifications))
	}
	if got := <-b.Notifications; got.ID != "$new" {
		t.Fatalf("wrong alert: %+v", got)
	}
	process("[" + fresh + "]")
	if len(b.Notifications) != 0 {
		t.Fatal("duplicate notification")
	}
	low := `[{"type":"m.tag","content":{"tags":{"m.lowpriority":{}}}}]`
	clearTags := `[{"type":"m.tag","content":{"tags":{}}}]`
	mute := `[{"type":"m.push_rules","content":{"global":{"override":[{"rule_id":"!room:test","enabled":true,"actions":["dont_notify"],"conditions":[{"kind":"event_match","key":"room_id","pattern":"!room:test"}]}]}}}]`
	unmute := `[{"type":"m.push_rules","content":{"global":{"override":[]}}}]`
	process("["+msg("$low", "@other:test", `{"msgtype":"m.text","body":"low priority"}`)+"]", low, "[]")
	process("["+msg("$muted", "@other:test", `{"msgtype":"m.text","body":"muted"}`)+"]", clearTags, mute)
	process("[" + msg("$still-muted", "@other:test", `{"msgtype":"m.text","body":"still muted"}`) + "]")
	if len(b.Notifications) != 0 {
		t.Fatal("low-priority or muted room notified")
	}
	if _, err := db.Message("$muted"); err != nil {
		t.Fatalf("muted message was not saved: %v", err)
	}
	process("["+msg("$unmuted", "@other:test", `{"msgtype":"m.text","body":"unmuted"}`)+"]", "[]", unmute)
	if len(b.Notifications) != 1 || (<-b.Notifications).ID != "$unmuted" {
		t.Fatal("alerts did not resume after unmuting")
	}
	historical := &event.Event{ID: "$history", RoomID: "!room:test", Sender: "@other:test", Type: event.EventMessage, Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "history"}}}
	b.handleEvent(context.Background(), historical)
	if len(b.Notifications) != 0 {
		t.Fatal("history notification")
	}
}
