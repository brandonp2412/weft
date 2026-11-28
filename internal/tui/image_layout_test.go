package tui

import (
	"github.com/charmbracelet/x/ansi"
	"image"
	"reflect"
	"strings"
	"testing"

	"maunium.net/go/mautrix/event"
	"weft/internal/store"
)

func TestTimelinePreviewKeepsMessagePositions(t *testing.T) {
	for _, sixel := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			info *event.FileInfo
		}{
			{"landscape", &event.FileInfo{Width: 1920, Height: 1080}},
			{"portrait", &event.FileInfo{Width: 1080, Height: 1920}},
			{"wide", &event.FileInfo{Width: 4000, Height: 200}},
			{"tiny", &event.FileInfo{Width: 8, Height: 8}},
			{"thumbnail", &event.FileInfo{ThumbnailInfo: &event.FileInfo{Width: 320, Height: 180}}},
			{"missing", nil},
			{"invalid", &event.FileInfo{Width: -1, Height: 200}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				m := demoModel(t)
				m.settings.ImagePreviews = true
				m.width, m.height = 120, 40
				m.sixelSupported, m.cellWidth, m.cellHeight = sixel, 8, 16
				m.messages = []store.Message{
					{ID: "$before", Body: "before"},
					{ID: "$image", Body: "image", Kind: "m.image", ImageInfo: tc.info, Pending: true},
					{ID: "$after", Body: "after"},
				}
				m.renderTimeline()
				expected := m.spans
				reserved := expected["$image"][1] - expected["$image"][0]
				if reserved <= 2 {
					t.Fatal("no space reserved for image")
				}
				m.messages[1].Pending = false
				for _, thumb := range []*image.NRGBA{nil, image.NewNRGBA(image.Rect(0, 0, 320, 180)), image.NewNRGBA(image.Rect(0, 0, 40, 100))} {
					m.previews["$image"] = thumb
					m.renderTimeline()
					if !reflect.DeepEqual(m.spans, expected) {
						t.Fatalf("sixel=%v: positions changed from %v to %v", sixel, expected, m.spans)
					}
					if sixel && thumb != nil && len(m.timelineTiles) != 1 {
						t.Fatal("loaded sixel preview not rendered")
					}
				}
				delete(m.previews, "$image")
				m.renderTimeline()
				if !reflect.DeepEqual(m.spans, expected) {
					t.Fatal("cache eviction changed message positions")
				}
			})
		}
	}
}

func TestTimelinePreviewReadsStoredMetadata(t *testing.T) {
	m := demoModel(t)
	raw := []byte(`{"type":"m.room.message","content":{"msgtype":"m.image","info":{"w":1920,"h":1080}}}`)
	if err := m.db.SaveMessage(store.Message{ID: "$dimensions", RoomID: m.current, Kind: "m.image", Raw: raw}, false); err != nil {
		t.Fatal(err)
	}
	msg, err := m.db.Message("$dimensions")
	if err != nil || msg.ImageInfo == nil || msg.ImageInfo.Width != 1920 || msg.ImageInfo.Height != 1080 {
		t.Fatalf("metadata missing: %v, %v", msg.ImageInfo, err)
	}
}

func TestImagePlaceholderCentered(t *testing.T) {
	m := demoModel(t)
	for _, sixel := range []bool{false, true} {
		m.sixelSupported, m.cellWidth, m.cellHeight = sixel, 8, 16
		lines, _ := m.timelinePreview(store.Message{ID: "$loading", Kind: "m.image", ImageInfo: &event.FileInfo{Width: 640, Height: 480}}, 60)
		for i, line := range lines {
			plain := strings.TrimPrefix(ansi.Strip(line), "    ")
			label := "Loading image…"
			if i == (len(lines)-1)/2 {
				left := len(plain) - len(strings.TrimLeft(plain, " "))
				right := len(plain) - len(strings.TrimRight(plain, " "))
				if strings.TrimSpace(plain) != label || right-left < 0 || right-left > 1 {
					t.Fatalf("not centered: %q", plain)
				}
			} else if strings.TrimSpace(plain) != "" {
				t.Fatalf("unexpected label on row %d", i)
			}
		}
	}
}
