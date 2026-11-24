package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	got, err := LoadSettings(path)
	if err != nil || got != DefaultSettings() {
		t.Fatalf("missing config: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"partial", "keyboard_mode = 'vi'\nread_receipts = false", true},
		{"unknown mode", "keyboard_mode = 'vim'", false},
		{"unknown key", "typo = true", false},
		{"bad type", "show_sidebar = 'yes'", false},
		{"bad syntax", "keyboard_mode = [", false},
		{"zero limit", "history_limit = 0", false},
		{"negative page", "history_page_size = -1", false},
		{"large scroll", "mouse_scroll_lines = 101", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadSettings(path)
			if (err == nil) != tc.valid {
				t.Fatalf("got %+v, %v", got, err)
			}
			if tc.valid && (got.KeyboardMode != "vi" || got.ReadReceipts || got.HistoryLimit != 150 || !got.TypingNotifications) {
				t.Fatalf("defaults/overrides: %+v", got)
			}
		})
	}
}

func TestSettingsPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got, want := SettingsPath(), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "weft", "config.toml"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
