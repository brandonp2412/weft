package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Settings contains user preferences, separate from account credentials.
type Settings struct {
	TimeFormat           string `toml:"time_format"`
	DesktopNotifications bool   `toml:"desktop_notifications"`
	NotificationPreviews bool   `toml:"notification_previews"`
	KeyboardMode         string `toml:"keyboard_mode"`
	ShowSidebar          bool   `toml:"show_sidebar"`
	HistoryLimit         int    `toml:"history_limit"`
	HistoryPageSize      int    `toml:"history_page_size"`
	MouseScrollLines     int    `toml:"mouse_scroll_lines"`
	TypingNotifications  bool   `toml:"typing_notifications"`
	ReadReceipts         bool   `toml:"read_receipts"`
}

func DefaultSettings() Settings {
	return Settings{TimeFormat: "24h", DesktopNotifications: true, KeyboardMode: "normal", ShowSidebar: true, HistoryLimit: 150, HistoryPageSize: 50, MouseScrollLines: 3, TypingNotifications: true, ReadReceipts: true}
}

func SettingsPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "weft", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "weft", "config.toml")
}

// LoadSettings uses defaults for missing files and omitted keys. Unknown keys
// are rejected so misspelled preferences do not silently go unused.
func LoadSettings(path string) (Settings, error) {
	s := DefaultSettings()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&s); err != nil {
		return s, fmt.Errorf("read config %s: %w", path, err)
	}
	if err = s.Validate(); err != nil {
		return s, fmt.Errorf("config %s: %w", path, err)
	}
	return s, nil
}

func (s Settings) Validate() error {
	if s.TimeFormat != "12h" && s.TimeFormat != "24h" {
		return fmt.Errorf("time_format must be 12h or 24h")
	}
	if s.KeyboardMode != "normal" && s.KeyboardMode != "vi" {
		return fmt.Errorf("keyboard_mode must be normal or vi")
	}
	if s.HistoryLimit < 1 || s.HistoryLimit > 10000 {
		return fmt.Errorf("history_limit must be between 1 and 10000")
	}
	if s.HistoryPageSize < 1 || s.HistoryPageSize > 10000 {
		return fmt.Errorf("history_page_size must be between 1 and 10000")
	}
	if s.MouseScrollLines < 1 || s.MouseScrollLines > 100 {
		return fmt.Errorf("mouse_scroll_lines must be between 1 and 100")
	}
	return nil
}
