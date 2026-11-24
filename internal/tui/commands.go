package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (m *Model) run(success string, fn func(context.Context) error) tea.Cmd {
	if m.demo {
		m.status = "Offline demo: sign in to use Matrix commands"
		return nil
	}
	if m.busy {
		m.status = "An operation is still running"
		return nil
	}
	m.busy = true
	m.status = "Working…"
	m.statusError = false
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
		defer cancel()
		return resultMsg{text: success, err: fn(ctx)}
	}
}
func split(s string) (string, string) {
	a, b, _ := strings.Cut(strings.TrimSpace(s), " ")
	return a, strings.TrimSpace(b)
}
func (m Model) target(ref string) (string, error) {
	if ref == "" || ref == "selected" {
		if m.selected != "" {
			return m.selected, nil
		}
		return "", errors.New("select a message with Ctrl+↑, or supply #number / $eventID")
	}
	if strings.HasPrefix(ref, "$") {
		return ref, nil
	}
	n, e := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if e != nil || n < 1 || n > len(m.messages) {
		return "", errors.New("invalid message number; select a message with Ctrl+↑ or use $eventID")
	}
	msg := m.messages[n-1]
	if msg.Pending {
		return "", errors.New("message is still queued")
	}
	return msg.ID, nil
}
func (m *Model) command(line string) tea.Cmd {
	command, arg := split(strings.TrimPrefix(line, "/"))
	room := m.current
	fail := func(text string) tea.Cmd { m.fail(errors.New(text)); return nil }
	switch command {
	case "q", "quit":
		return tea.Quit
	case "help":
		m.mode = "help"
		m.input.Blur()
		return nil
	case "rooms":
		m.openPrompt("rooms")
		return nil
	case "back":
		m.thread = ""
		m.reply = ""
		m.edit = ""
		m.reload()
		return nil
	case "search":
		m.openPrompt("search")
		m.prompt.SetValue(arg)
		return nil
	case "history":
		return m.history()
	case "whoami":
		m.mode = "details"
		m.details = m.userID()
		if !m.demo {
			m.details += fmt.Sprintf("\nDevice: %s\nHomeserver: %s\nFingerprint: %s", m.backend.Client.DeviceID, m.backend.Client.HomeserverURL, m.backend.Crypto.Machine().Fingerprint())
		}
		return nil
	case "recover", "recover-passphrase", "setup-keys":
		if m.demo {
			return fail("Recovery is available after login")
		}
		m.openPrompt(command)
		return nil
	case "verify":
		if m.demo {
			return fail("Verification is available after login")
		}
		user := m.backend.Client.UserID
		if arg != "" {
			user = id.UserID(arg)
		}
		return m.run("Verification requested; accept it on the other device", func(ctx context.Context) error { _, e := m.backend.StartVerification(ctx, user); return e })
	case "accept", "confirm", "cancel":
		if m.demo || m.verification == nil {
			return fail("No active verification")
		}
		txn := m.verification.Txn
		return m.run("Verification updated", func(ctx context.Context) error {
			switch command {
			case "accept":
				return m.backend.AcceptVerification(ctx, txn)
			case "confirm":
				if m.verification.Stage != "sas" {
					return errors.New("wait for verification symbols before confirming")
				}
				return m.backend.ConfirmVerification(ctx, txn)
			default:
				return m.backend.CancelVerification(ctx, txn, "m.user", "Cancelled by user")
			}
		})
	case "retry":
		if m.demo {
			return nil
		}
		m.backend.Wake()
		return m.run("Retried encrypted messages and queued sends", m.backend.RetryDecrypt)
	case "discard":
		if arg == "" {
			return fail("Usage: /discard #number (queued messages only)")
		}
		n, e := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if e != nil || n < 1 || n > len(m.messages) || !m.messages[n-1].Pending {
			return fail("Choose a queued message")
		}
		if e = m.db.RemovePending(m.messages[n-1].ID); e != nil {
			m.fail(e)
		} else {
			m.reload()
			m.status = "Queued message discarded"
		}
		return nil
	case "join":
		if arg == "" {
			arg = room
		}
		if arg == "" {
			return fail("Usage: /join #alias:server or !roomID")
		}
		join := arg
		return m.run("Joined room", func(ctx context.Context) error {
			resp, e := m.backend.Client.JoinRoom(ctx, join, nil)
			if e != nil {
				return e
			}
			if e = m.db.RoomField(resp.RoomID.String(), "membership", "join"); e != nil {
				return e
			}
			m.backend.Changed()
			return nil
		})
	case "leave":
		if room == "" {
			return fail("Select a room first")
		}
		if arg != "confirm" {
			return fail("Type /leave confirm to leave this room")
		}
		return m.run("Left room", func(ctx context.Context) error {
			_, e := m.backend.Client.LeaveRoom(ctx, id.RoomID(room))
			if e == nil {
				e = m.db.RoomField(room, "membership", "leave")
			}
			return e
		})
	case "invite":
		if room == "" || arg == "" {
			return fail("Usage: /invite @user:server")
		}
		return m.run("Invitation sent", func(ctx context.Context) error {
			_, e := m.backend.Client.InviteUser(ctx, id.RoomID(room), &mautrix.ReqInviteUser{UserID: id.UserID(arg)})
			return e
		})
	case "dm", "create":
		if arg == "" {
			return fail("Usage: /dm @user:server or /create room name")
		}
		req := &mautrix.ReqCreateRoom{Preset: "private_chat", Visibility: "private", InitialState: []*event.Event{{Type: event.StateEncryption, StateKey: ptr(""), Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}}}}}
		if command == "dm" {
			if _, _, e := id.UserID(arg).Parse(); e != nil {
				return fail("Use a full @user:server Matrix ID")
			}
			req.IsDirect = true
			req.Invite = []id.UserID{id.UserID(arg)}
		} else {
			req.Name = arg
		}
		if m.demo {
			return fail("Room creation is available after login")
		}
		if m.busy {
			return fail("An operation is still running")
		}
		m.busy = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
			defer cancel()
			resp, e := m.backend.Client.CreateRoom(ctx, req)
			if e != nil {
				return resultMsg{err: e}
			}
			_ = m.db.RoomField(resp.RoomID.String(), "membership", "join")
			_ = m.db.RoomField(resp.RoomID.String(), "encrypted", true)
			_ = m.db.RoomField(resp.RoomID.String(), "name", arg)
			if req.IsDirect {
				var dm map[id.UserID][]id.RoomID
				_ = m.backend.Client.GetAccountData(ctx, event.AccountDataDirectChats.Type, &dm)
				if dm == nil {
					dm = map[id.UserID][]id.RoomID{}
				}
				dm[id.UserID(arg)] = append(dm[id.UserID(arg)], resp.RoomID)
				_ = m.backend.Client.SetAccountData(ctx, event.AccountDataDirectChats.Type, dm)
			}
			return resultMsg{text: "Created encrypted room", room: resp.RoomID.String()}
		}
	case "name", "topic":
		if room == "" {
			return fail("Select a room first")
		}
		typ := event.StateRoomName
		var content any = &event.RoomNameEventContent{Name: arg}
		if command == "topic" {
			typ = event.StateTopic
			content = &event.TopicEventContent{Topic: arg}
		}
		return m.run("Room "+command+" updated", func(ctx context.Context) error {
			_, e := m.backend.Client.SendStateEvent(ctx, id.RoomID(room), typ, "", content)
			return e
		})
	case "members":
		return m.showDetails(func(ctx context.Context) (string, error) {
			resp, e := m.backend.Client.Members(ctx, id.RoomID(room))
			if e != nil {
				return "", e
			}
			lines := []string{"Members"}
			for _, evt := range resp.Chunk {
				_ = evt.Content.ParseRaw(evt.Type)
				c := evt.Content.AsMember()
				if c.Membership == event.MembershipJoin {
					lines = append(lines, c.Displayname+"  "+evt.GetStateKey())
				}
			}
			return strings.Join(lines, "\n"), nil
		})
	case "devices":
		return m.showDetails(func(ctx context.Context) (string, error) {
			devices, e := m.backend.Devices(ctx)
			if e != nil {
				return "", e
			}
			lines := []string{"Your devices"}
			for device, d := range devices {
				lines = append(lines, fmt.Sprintf("%s  %s  trust=%s", device, d.Name, d.Trust))
			}
			return strings.Join(lines, "\n"), nil
		})
	case "read":
		target := ""
		for i := len(m.messages) - 1; i >= 0; i-- {
			if !m.messages[i].Pending {
				target = m.messages[i].ID
				break
			}
		}
		return m.run("Marked read", func(ctx context.Context) error { return m.backend.MarkRead(ctx, room, target) })
	case "me":
		if arg == "" {
			return fail("Usage: /me action")
		}
		if m.demo {
			return fail("Sign in to send messages")
		}
		if e := m.backend.Text(room, arg, true, "", m.thread, ""); e != nil {
			m.fail(e)
		}
		m.reload()
		return nil
	case "reply", "thread", "edit", "react", "redact", "download", "embed":
		ref, body := split(arg)
		target, e := m.target(ref)
		if e != nil {
			m.fail(e)
			return nil
		}
		switch command {
		case "reply", "thread", "edit":
			if body != "" {
				if m.demo {
					return fail("Sign in to send messages")
				}
				reply, thread, edit := "", "", ""
				switch command {
				case "reply":
					reply = target
				case "thread":
					thread = target
				case "edit":
					edit = target
				}
				if e = m.backend.Text(room, body, false, reply, thread, edit); e != nil {
					m.fail(e)
				}
				m.reload()
				return nil
			}
			switch command {
			case "reply":
				m.reply = target
			case "thread":
				m.thread = target
				m.scroll = 0
				m.reload()
			case "edit":
				msg, e := m.db.Message(target)
				if e != nil {
					m.fail(e)
					return nil
				}
				if msg.Sender != m.userID() {
					return fail("You can only edit your own messages")
				}
				m.edit = target
				m.input.SetValue(msg.Body)
			}
			return nil
		case "react":
			if body == "" {
				return fail("Usage: /react #number emoji")
			}
			return m.run("Reaction sent", func(ctx context.Context) error { return m.backend.React(ctx, room, target, body) })
		case "redact":
			return m.run("Message deleted", func(ctx context.Context) error { return m.backend.Redact(ctx, room, target, body) })
		case "embed":
			return m.run("Image queued", func(ctx context.Context) error { return m.backend.Embed(room, target, body) })
		case "download":
			if body == "" {
				return fail("Usage: /download #number /path/to/save")
			}
			return m.run("Saved attachment to "+body, func(ctx context.Context) error { return m.backend.Download(ctx, target, body) })
		}
	case "image":
		if arg == "" {
			return fail("Usage: /image /path/to/image or /image mxc://server/media [caption]")
		}
		if strings.HasPrefix(arg, "mxc://") {
			source, caption := split(arg)
			return m.run("Image queued", func(ctx context.Context) error { return m.backend.Image(room, source, caption) })
		}
		return m.run("Image queued", func(ctx context.Context) error { return m.backend.Upload(ctx, room, arg) })
	case "upload":
		if path, ok := droppedFile(arg); ok {
			arg = path
		}
		if arg == "" {
			return fail("Usage: /upload /path/to/file")
		}
		return m.run("Attachment queued", func(ctx context.Context) error { return m.backend.Upload(ctx, room, arg) })
	default:
		return fail("Unknown command /" + command + ". Type /help for commands.")
	}
	return nil
}
func ptr(s string) *string { return &s }
func (m *Model) showDetails(fn func(context.Context) (string, error)) tea.Cmd {
	if m.demo {
		m.status = "Sign in to use this command"
		return nil
	}
	if m.busy {
		m.status = "An operation is still running"
		return nil
	}
	m.busy = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		details, e := fn(ctx)
		return resultMsg{details: details, err: e}
	}
}
