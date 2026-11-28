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
		{"12 hour", "keyboard_mode = 'vi'\nread_receipts = false\ntime_format = '12h'", true},
		{"invalid time format", "time_format = '12'", false},
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

func TestQueuedIndicatorDelayConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if DefaultSettings().QueuedIndicatorDelayMS != 1000 {
		t.Fatal("unexpected default")
	}
	for _, tc := range []struct {
		text  string
		want  int
		valid bool
	}{
		{"queued_indicator_delay_ms = 0", 0, true},
		{"queued_indicator_delay_ms = 250", 250, true},
		{"queued_indicator_delay_ms = -1", 0, false},
	} {
		if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadSettings(path)
		if (err == nil) != tc.valid || tc.valid && got.QueuedIndicatorDelayMS != tc.want {
			t.Fatalf("%q: %+v %v", tc.text, got, err)
		}
	}
}

func TestShowSendingIndicatorsConfig(t *testing.T) {
	if !DefaultSettings().ShowSendingIndicators {
		t.Fatal("sending indicators should default to enabled")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("show_sending_indicators = false"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(path)
	if err != nil || got.ShowSendingIndicators {
		t.Fatalf("disabled setting: %+v %v", got, err)
	}
}
