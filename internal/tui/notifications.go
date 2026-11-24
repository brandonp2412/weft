package tui

import (
	"context"
	"fmt"
	"html"
	"os/exec"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"weft/internal/store"
)

type notificationMsg store.Message

func (m Model) waitNotifications() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.backend.Notifications:
			return notificationMsg(msg)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m Model) shouldNotify(msg store.Message) bool {
	return !m.demo && m.settings.DesktopNotifications && msg.Sender != m.userID() &&
		!(m.focused && msg.RoomID == m.current && m.scroll == 0 && (m.thread == "" || msg.ThreadRoot == m.thread))
}

func (m Model) desktopNotification(msg store.Message) tea.Cmd {
	if !m.shouldNotify(msg) {
		return nil
	}
	room, err := m.db.Room(msg.RoomID)
	if err != nil {
		return nil
	}
	title := room.Name
	if title == "" {
		title = msg.RoomID
	}
	body := "New message"
	if m.settings.NotificationPreviews && msg.Kind != "encrypted" {
		body = m.db.MemberName(msg.RoomID, msg.Sender) + ": " + msg.Body
	}
	title, body = notificationText(title), html.EscapeString(notificationText(body))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "notify-send", "--app-name=weft", "--icon=mail-unread", "--", title, body).Run(); err != nil {
			return resultMsg{err: fmt.Errorf("desktop notification: %w", err)}
		}
		return nil
	}
}

func notificationText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300]) + "…"
	}
	return s
}
