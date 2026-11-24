package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"maunium.net/go/mautrix"
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

func TestEncryptedImageThumbnailAndDownload(t *testing.T) {
	original, thumb := []byte("original image"), []byte("thumbnail")
	ciphertext := slices.Clone(thumb)
	encrypted := attachment.NewEncryptedFile()
	encrypted.EncryptInPlace(ciphertext)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/thumb"):
			_, _ = w.Write(ciphertext)
		case strings.HasSuffix(r.URL.Path, "/full"):
			_, _ = w.Write(original)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := mautrix.NewClient(server.URL, "@user:test", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.EnsureRoom("!room:test"); err != nil {
		t.Fatal(err)
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgImage, Body: "photo.png", URL: "mxc://test/full",
		Info: &event.FileInfo{
			ThumbnailFile: &event.EncryptedFileInfo{EncryptedFile: *encrypted, URL: "mxc://test/thumb"},
		},
	}
	raw, err := json.Marshal(&event.Event{Type: event.EventMessage, Content: event.Content{Parsed: content}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveMessage(store.Message{ID: "$image", RoomID: "!room:test", Kind: "m.image", Body: content.Body, Raw: raw}, false); err != nil {
		t.Fatal(err)
	}
	b := &Backend{Store: db, Client: client}
	got, err := b.ImagePreview(t.Context(), "$image", 8<<20, true)
	if err != nil || !slices.Equal(got, thumb) {
		t.Fatalf("image preview: %q, %v", got, err)
	}
	got, err = b.ImagePreview(t.Context(), "$image", 8<<20, false)
	if err != nil || !slices.Equal(got, original) {
		t.Fatalf("original image preview: %q, %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "original")
	if err := b.Download(t.Context(), "$image", path); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || !slices.Equal(got, original) {
		t.Fatalf("download: %q, %v", got, err)
	}
}
