package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maunium.net/go/mautrix"
	"weft/internal/matrix"
	"weft/internal/store"
)

func TestNotificationVisibility(t *testing.T) {
	m := demoModel(t)
	m.demo = false
	m.backend = &matrix.Backend{Client: &mautrix.Client{UserID: "@me:test"}}
	msg := store.Message{RoomID: m.current, Sender: "@other:test"}
	if m.shouldNotify(msg) {
		t.Fatal("visible room alert")
	}
	m.focused = false
	if !m.shouldNotify(msg) {
		t.Fatal("unfocused alert suppressed")
	}
	m.focused = true
	m.scroll = 1
	if !m.shouldNotify(msg) {
		t.Fatal("scrolled room alert suppressed")
	}
	m.scroll = 0
	msg.RoomID = "!other:test"
	if !m.shouldNotify(msg) {
		t.Fatal("background room alert suppressed")
	}
	msg.Sender = "@me:test"
	if m.shouldNotify(msg) {
		t.Fatal("own message alert")
	}
	msg.Sender = "@other:test"
	m.settings.DesktopNotifications = false
	if m.shouldNotify(msg) {
		t.Fatal("disabled alert")
	}
}

func TestNotificationDelivery(t *testing.T) {
	m := demoModel(t)
	m.demo = false
	m.focused = false
	m.backend = &matrix.Backend{Client: &mautrix.Client{UserID: "@me:test"}}
	dir := t.TempDir()
	output := filepath.Join(dir, "args")
	t.Setenv("NOTIFICATION_TEST_OUTPUT", output)
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NOTIFICATION_TEST_OUTPUT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	msg := store.Message{RoomID: m.current, Sender: "@other:test", Body: "<b>private</b>\x1b\n", Kind: "m.text"}
	deliver := func() string {
		t.Helper()
		cmd := m.desktopNotification(msg)
		if cmd == nil {
			t.Fatal("missing notification command")
		}
		if result := cmd(); result != nil {
			t.Fatalf("delivery failed: %+v", result)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := deliver(); !strings.Contains(got, "New message") || strings.Contains(got, "private") {
		t.Fatalf("preview leaked: %s", got)
	}
	m.settings.NotificationPreviews = true
	if got := deliver(); !strings.Contains(got, "&lt;b&gt;private&lt;/b&gt;") || strings.Contains(got, "\x1b") {
		t.Fatalf("unsafe preview: %s", got)
	}
}
