package store

import (
	"maunium.net/go/mautrix/event"
	"testing"
)

func TestTimelineEchoReplacesPending(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	room := "!test:example.org"
	if err = s.Enqueue(room, "txn", event.MessageEventContent{MsgType: event.MsgText, Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	msgs, queued, err := s.Timeline(room, 100)
	if err != nil || len(msgs) != 0 || len(queued) != 1 {
		t.Fatalf("before echo: %v %v %v", msgs, queued, err)
	}
	msg := Message{ID: "$confirmed", RoomID: room, Sender: "@me:example.org", Body: "hello", Kind: "m.text", TS: queued[0].TS}
	if err = s.SaveMessageEcho(msg, false, "txn"); err != nil {
		t.Fatal(err)
	}
	msgs, queued, err = s.Timeline(room, 100)
	if err != nil || len(msgs) != 1 || len(queued) != 0 {
		t.Fatalf("after echo: %v %v %v", msgs, queued, err)
	}
}
