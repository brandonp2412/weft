package store

import "testing"

func TestRoomPinsPersistAndSortBeforeRecentRooms(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	for _, msg := range []Message{
		{ID: "$old", RoomID: "!old:x", Sender: "@a:x", Kind: "m.text", TS: 1},
		{ID: "$new", RoomID: "!new:x", Sender: "@a:x", Kind: "m.text", TS: 2},
	} {
		if err := s.SaveMessage(msg, false); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := s.PinRoom("!old:x", true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := s.Rooms()
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 || rooms[0].ID != "!old:x" || !rooms[0].Pinned {
		t.Fatalf("pins lost: %+v", rooms)
	}
	room, err := s.Room("!old:x")
	if err != nil || !room.Pinned {
		t.Fatalf("room pin: %+v, %v", room, err)
	}
	// New activity must not move an unpinned room ahead of a pin.
	if err := s.SaveMessage(Message{ID: "$latest", RoomID: "!new:x", Sender: "@a:x", Kind: "m.text", TS: 3}, false); err != nil {
		t.Fatal(err)
	}
	rooms, err = s.Rooms()
	if err != nil || rooms[0].ID != "!old:x" {
		t.Fatalf("activity displaced pin: %+v, %v", rooms, err)
	}
	if err := s.PinRoom("!old:x", false); err != nil {
		t.Fatal(err)
	}
	rooms, err = s.Rooms()
	if err != nil || rooms[0].ID != "!new:x" || rooms[1].Pinned {
		t.Fatalf("unpin: %+v, %v", rooms, err)
	}
}
