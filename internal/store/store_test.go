package store

import (
	"context"
	"maunium.net/go/mautrix/event"
	"testing"
)

func TestRelationsArriveBeforeMessages(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, r := range []struct {
		id, sender, kind, body string
		ts                     int64
	}{{"$evil", "@mallory:x", "edit", "forged", 50}, {"$edit1", "@alice:x", "edit", "first edit", 20}, {"$edit2", "@alice:x", "edit", "second edit", 30}, {"$reaction", "@bob:x", "reaction", "👍", 40}} {
		if e = s.Relation(r.id, "!room:x", "$original", r.sender, r.kind, r.body, r.ts); e != nil {
			t.Fatal(e)
		}
	}
	m := Message{ID: "$original", RoomID: "!room:x", Sender: "@alice:x", Body: "original", Kind: "m.text", TS: 10}
	if e = s.SaveMessage(m, false); e != nil {
		t.Fatal(e)
	}
	got, e := s.Message(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Body != "second edit" || !got.Edited || got.Reactions != "👍 1" {
		t.Fatalf("bad projection: %+v", got)
	}
	if e = s.Redact("$edit2", "$redact-edit", "!room:x", "@alice:x", 60); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Message(m.ID)
	if got.Body != "first edit" {
		t.Fatalf("redacted edit still applied: %+v", got)
	}
	if e = s.Redact("$reaction", "$redact-reaction", "!room:x", "@bob:x", 70); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Message(m.ID)
	if got.Reactions != "" {
		t.Fatal("redacted reaction still visible")
	}
	if e = s.Redact(m.ID, "$redact-original", "!room:x", "@alice:x", 80); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveMessage(m, false); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Message(m.ID)
	if !got.Redacted || got.Body != "[message deleted]" {
		t.Fatal("replayed event resurrected a redaction")
	}
	results, e := s.Search(m.RoomID, "original")
	if e != nil {
		t.Fatal(e)
	}
	if len(results) != 0 {
		t.Fatal("search exposed redacted text")
	}
}
func TestOutboxSurvivesRestartAndSyncCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Enqueue("!room:x", "txn", event.MessageEventContent{MsgType: event.MsgText, Body: "durable"}); e != nil {
		t.Fatal(e)
	}
	if e = s.Put("next_batch", "committed"); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveNextBatch(context.Background(), "@user:x", "not-processed"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := s.Pending()
	if e != nil {
		t.Fatal(e)
	}
	if len(p) != 1 || p[0].Content.Body != "durable" {
		t.Fatal("pending message lost")
	}
	token, _ := s.LoadNextBatch(context.Background(), "@user:x")
	if token != "committed" {
		t.Fatal("unprocessed sync checkpoint persisted")
	}
}
func TestLateCiphertextDoesNotReplacePlaintext(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := Message{ID: "$a", RoomID: "!r:x", Sender: "@a:x", Body: "decrypted", Kind: "m.text", TS: 1}
	_ = s.SaveMessage(m, false)
	m.Kind = "encrypted"
	m.Body = "ciphertext"
	_ = s.SaveMessage(m, false)
	got, e := s.Message(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Body != "decrypted" {
		t.Fatal("decrypted content was overwritten")
	}
}

func TestRelationsCannotCrossRooms(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := Message{ID: "$target", RoomID: "!one:x", Sender: "@alice:x", Body: "original", Kind: "m.text", TS: 1}
	if e = s.SaveMessage(m, false); e != nil {
		t.Fatal(e)
	}
	_ = s.Relation("$cross-edit", "!two:x", m.ID, m.Sender, "edit", "forged", 2)
	_ = s.Relation("$cross-react", "!two:x", m.ID, "@mallory:x", "reaction", "bad", 3)
	_ = s.Redact(m.ID, "$cross-delete", "!two:x", "@mallory:x", 4)
	got, e := s.Message(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Body != "original" || got.Edited || got.Redacted || got.Reactions != "" {
		t.Fatalf("cross-room event changed target: %+v", got)
	}
}
