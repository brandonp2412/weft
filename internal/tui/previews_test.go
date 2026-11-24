package tui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStagedImagePreviewSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	err = png.Encode(f, img)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("writing image: %v, %v", err, closeErr)
	}
	m := demoModel(t)
	m.input.SetValue("draft")
	_, cmd := m.Update(tea.PasteMsg{Content: path})
	if cmd != nil {
		t.Fatal("decoded an attachment with previews disabled")
	}
	m.settings.ImagePreviews = true
	updated, cmd := m.Update(tea.PasteMsg{Content: path})
	if cmd == nil {
		t.Fatal("preview was not requested")
	}
	preview := cmd()
	decoded := preview.(imagePreviewMsg).image
	if decoded == nil || decoded.Bounds() != img.Bounds() {
		t.Fatal("preview did not preserve the dimensions of a small source image")
	}
	updated, _ = updated.Update(preview)
	if !strings.Contains(updated.View().Content, "▀") || updated.(Model).input.Value() != "draft" {
		t.Fatal("staged preview or draft is missing")
	}
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	updated, _ = updated.Update(preview)
	if strings.Contains(updated.View().Content, "▀") {
		t.Fatal("discarded attachment preview reappeared")
	}
}

func TestSixelOutputDrawsAfterTextAndClearsRemovedImage(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	out := &sixelOutput{File: file}
	img := image.NewNRGBA(image.Rect(0, 0, 8, 16))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	tile := sixelTile{image: img, width: 8, height: 16, cellWidth: 8, cellHeight: 16}
	frame := []sixelPlacement{{tile: tile, x: 4, y: 2, rows: 1}}
	start := "\x1b]2;" + out.frameTitle(frame) + "\x07first frame"
	remove := "\x1b]2;" + out.frameTitle(nil) + "\x07second frame"
	for _, text := range []string{start, remove} {
		if written, err := out.Write([]byte(text)); err != nil || written != len(text) {
			t.Fatalf("write returned %d, %v", written, err)
		}
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	draw := strings.Index(output, "\x1b[3;5H\x1bP0;1q")
	clear := strings.Index(output, "\x1b[3;5H\x1b[1X")
	if strings.Contains(output, graphicsTitle) || draw < strings.Index(output, "first frame") ||
		clear <= draw || clear > strings.Index(output, "second frame") || strings.Count(output, "\x1bP0;1q") != 1 {
		t.Fatalf("unexpected terminal output: %q", output)
	}
}
