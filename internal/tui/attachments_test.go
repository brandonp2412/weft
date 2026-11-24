package tui

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDroppedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my photo.png")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	for _, input := range []string{path, "'" + path + "' ", "\"" + path + "\"", strings.ReplaceAll(path, " ", "\\ "), uri} {
		if got, ok := droppedFile(input); !ok || got != path {
			t.Errorf("%q: got %q, %v", input, got, ok)
		}
	}
	for _, input := range []string{"hello there", "photo.png", filepath.Dir(path), path + ".missing", "file://remote" + path, "'" + path, "'" + path + "' '/another/file'", "$(touch /tmp/weft-should-not-run)"} {
		if got, ok := droppedFile(input); ok {
			t.Errorf("unexpected attachment %q for %q", got, input)
		}
	}
}

func TestDropStagesUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my photo.png")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "recover", "search"} {
		m := demoModel(t)
		if mode != "" {
			m.openPrompt(mode)
		}
		updated, _ := m.Update(tea.PasteMsg{Content: "'" + path + "'"})
		m = updated.(Model)
		if mode == "" {
			if m.input.Value() != "/upload "+path {
				t.Fatalf("drop not staged: %q", m.input.Value())
			}
			if m.busy {
				t.Fatal("drop started upload before Enter")
			}
		} else if m.input.Value() != "" {
			t.Fatal("modal paste staged an attachment")
		}
	}
	m := demoModel(t)
	m.input.SetValue("draft: ")
	updated, _ := m.Update(tea.PasteMsg{Content: path})
	m = updated.(Model)
	if m.input.Value() != "draft: " || m.attachment == nil || m.attachment.path != path {
		t.Fatal("drop did not stage the file separately from the draft")
	}
}

func TestUnbracketedDropOnEnter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	m := demoModel(t)
	m.input.SetValue("'" + path + "'")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.status, "Matrix commands") {
		t.Fatalf("drop was treated as text: %q", m.status)
	}
}

func TestFootDropWithApostrophe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "today's photo.png")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(path, "'", "\\'")
	for _, content := range []string{escaped, "'" + escaped + "'"} {
		m := demoModel(t)
		updated, _ := m.Update(tea.PasteMsg{Content: content})
		if got := updated.(Model).input.Value(); got != "/upload "+path {
			t.Fatalf("drop staged %q", got)
		}
	}
}

func TestClipboardImageAndBinaryPaste(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  --list-types) printf 'text/plain\nimage/png\n' ;;
  '--no-newline --type image/png') printf '\211PNG\r\n\032\n\000' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "test")

	m := demoModel(t)
	m.input.SetValue("draft")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+V did not read the clipboard")
	}
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.attachment == nil || m.attachment.mimeType != "image/png" ||
		!bytes.Equal(m.attachment.data, []byte("\x89PNG\r\n\x1a\n\x00")) {
		t.Fatal("Ctrl+V did not stage the original image bytes")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.attachment != nil {
		t.Fatal("Escape did not discard the staged image")
	}
	// The terminal decoder removes the leading invalid UTF-8 byte of a PNG.
	updated, _ = m.Update(tea.PasteMsg{Content: "PNG\r\n\x1a\n\x00IHDR"})
	m = updated.(Model)
	if m.input.Value() != "draft" || !m.statusError {
		t.Fatal("binary paste was not rejected while preserving the draft")
	}
}
