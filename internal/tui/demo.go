package tui

import (
	"fmt"
	"time"
	"weft/internal/store"
)

func SeedDemo(db *store.Store) error {
	rooms := []store.Room{{ID: "!studio:weft.local", Name: "The studio", Topic: "A place for ideas, work in progress, and the occasional tangent.", Encrypted: true, Unread: 3}, {ID: "!matrix:weft.local", Name: "Matrix development", Topic: "Building things that bring people together.", Unread: 12}, {ID: "!garden:weft.local", Name: "The garden", Topic: "Small things, growing slowly.", Encrypted: true}, {ID: "!alex:weft.local", Name: "Alex", Encrypted: true}}
	now := time.Now().Add(-45 * time.Minute)
	for i, r := range rooms {
		for field, value := range map[string]any{"name": r.Name, "topic": r.Topic, "membership": "join", "encrypted": r.Encrypted, "unread": r.Unread} {
			if e := db.RoomField(r.ID, field, value); e != nil {
				return e
			}
		}
		_ = db.SetMember(r.ID, "@alex:weft.local", "Alex")
		_ = db.SetMember(r.ID, "@you:weft.local", "You")
		_ = db.SetMember(r.ID, "@sam:weft.local", "Sam")
		if i > 0 {
			_ = db.SaveMessage(store.Message{ID: fmt.Sprintf("$demo-room-%d", i), RoomID: r.ID, Sender: "@alex:weft.local", Body: "Good to see you here. Make yourself at home.", Kind: "m.text", TS: now.Add(-time.Duration(i) * time.Hour).UnixMilli(), Encrypted: r.Encrypted}, false)
		}
	}
	texts := []struct{ sender, body string }{{"@alex:weft.local", "Morning! I’ve been thinking about a quieter place for our conversations."}, {"@sam:weft.local", "Something that feels at home in the terminal. Just rooms, people, and the conversation."}, {"@you:weft.local", "That’s the idea behind weft. A small thread connecting everything."}, {"@alex:weft.local", "The name fits. How does it handle encrypted rooms?"}, {"@you:weft.local", "Messages and files are encrypted through mautrix-go. You can verify this device with another client, or restore your keys with /recover."}, {"@sam:weft.local", "And the little things: replies, reactions, editing a typo. Those make it feel like a real conversation."}, {"@alex:weft.local", "Try Ctrl+K to find a room, or Ctrl+↑ to select a message. PgUp takes you back through history."}, {"@you:weft.local", "I like that there’s room to breathe. No noise, just the people you came here to talk to."}, {"@sam:weft.local", "There’s a keyboard guide under F1. I’m going to leave this open while I work. 🌿"}}
	for i, t := range texts {
		e := db.SaveMessage(store.Message{ID: fmt.Sprintf("$demo-%d", i+1), RoomID: rooms[0].ID, Sender: t.sender, Body: t.body, Kind: "m.text", TS: now.Add(time.Duration(i) * 5 * time.Minute).UnixMilli(), Encrypted: true}, false)
		if e != nil {
			return e
		}
	}
	_ = db.Relation("$reaction1", rooms[0].ID, "$demo-3", "@alex:weft.local", "reaction", "🧶", now.UnixMilli())
	_ = db.Relation("$reaction2", rooms[0].ID, "$demo-3", "@sam:weft.local", "reaction", "🧶", now.UnixMilli())
	return nil
}
