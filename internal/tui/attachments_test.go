package tui

import (
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
	if !strings.HasPrefix(m.input.Value(), "draft: ") || strings.HasPrefix(m.input.Value(), "/upload ") {
		t.Fatal("drop replaced draft")
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
