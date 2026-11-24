package matrix

import (
	"encoding/json"
	"reflect"
	"testing"

	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"weft/internal/store"
)

func TestEmbedEncryptedImage(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.EnsureRoom("!room:test"); err != nil {
		t.Fatal(err)
	}
	if err = db.RoomField("!room:test", "membership", "join"); err != nil {
		t.Fatal(err)
	}
	b := &Backend{Store: db, wake: make(chan struct{}, 1), Updates: make(chan struct{}, 1)}
	encrypted := attachment.NewEncryptedFile()
	encrypted.EncryptInPlace([]byte("image bytes"))
	original := event.MessageEventContent{MsgType: event.MsgImage, Body: "photo.png", File: &event.EncryptedFileInfo{EncryptedFile: *encrypted, URL: "mxc://test/image"}, Info: &event.FileInfo{Width: 640, Height: 480}, RelatesTo: (&event.RelatesTo{}).SetReplyTo("$reply")}
	raw, err := json.Marshal(&event.Event{Type: event.EventMessage, Content: event.Content{Parsed: &original}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveMessage(store.Message{ID: "$image", RoomID: "!room:test", Kind: "m.image", Body: original.Body, Raw: raw}, false); err != nil {
		t.Fatal(err)
	}
	if err = b.Embed("!room:test", "$image", "New caption"); err != nil {
		t.Fatal(err)
	}
	pending, err := db.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("queued %d images", len(pending))
	}
	c := pending[0].Content
	if c.File == nil || c.File.URL != original.File.URL || !reflect.DeepEqual(c.File.Key, original.File.Key) || c.Info.Width != 640 || c.Body != "New caption" || c.RelatesTo != nil {
		t.Fatalf("image content lost or relation copied: %+v", c)
	}
	for _, source := range []string{"https://example.org/photo.png", "mxc://", "/tmp/photo.png"} {
		if err = b.Image("!room:test", source, ""); err == nil {
			t.Errorf("accepted %q", source)
		}
	}
	if err = b.Image("!room:test", "mxc://test/existing", "Existing"); err != nil {
		t.Fatal(err)
	}
}
