package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"weft/internal/config"
	"weft/internal/matrix"
	"weft/internal/store"
)

type refreshMsg struct{}
type noticeMsg matrix.Notice
type verifyMsg matrix.Verification
type resultMsg struct {
	text    string
	err     error
	room    string
	history bool
	details string
}
type Model struct {
	settings            config.Settings
	viInsert            bool
	viPending           string
	viCount             int
	viRegister          string
	viLinewise          bool
	viUndo              []viSnapshot
	viRedo              []viSnapshot
	focused             bool
	lastRead            map[string]string
	backend             *matrix.Backend
	db                  *store.Store
	demo                bool
	width, height       int
	rooms               []store.Room
	messages            []store.Message
	names               map[string]string
	current             string
	selected            string
	input               textarea.Model
	prompt              textinput.Model
	mode                string
	promptIndex         int
	completionIndex     int
	completionDismissed string
	details             string
	status              string
	statusError         bool
	connection          string
	verification        *matrix.Verification
	busy                bool
	historyBusy         bool
	limit               int
	scroll              int
	rendered            []string
	spans               map[string][2]int
	reply, thread, edit string
	drafts              map[string]string
	search              []store.Message
	lastTyping          time.Time
	ctx                 context.Context
}

func New(ctx context.Context, b *matrix.Backend, db *store.Store, demo bool) Model {
	return NewWithSettings(ctx, b, db, demo, config.DefaultSettings())
}

func NewWithSettings(ctx context.Context, b *matrix.Backend, db *store.Store, demo bool, settings config.Settings) Model {
	if b != nil {
		ctx = b.Context()
	}
	input := textarea.New()
	input.Placeholder = "Write a message…  /help for commands"
	input.ShowLineNumbers = false
	input.Prompt = "  "
	input.SetHeight(3)
	input.CharLimit = 32000
	input.SetVirtualCursor(true)
	input.Focus()
	prompt := textinput.New()
	prompt.Prompt = "  › "
	prompt.SetVirtualCursor(true)
	m := Model{settings: settings, focused: true, lastRead: map[string]string{}, backend: b, db: db, demo: demo, input: input, prompt: prompt, width: 100, height: 30, limit: settings.HistoryLimit, names: map[string]string{}, drafts: map[string]string{}, spans: map[string][2]int{}, current: db.Get("selected_room"), ctx: ctx, connection: "Connecting…", status: "Ctrl+K rooms · Alt+↑/↓ switch · PgUp history · /help"}
	if settings.KeyboardMode == "vi" {
		m.input.Blur()
		m.status = "i compose · j/k chats or draft lines · Ctrl+↑/↓ messages · / search"
	}
	if demo {
		m.connection = "Offline demo"
	}
	m.reload()
	return m
}
func (m Model) Init() tea.Cmd {
	if m.demo {
		return textarea.Blink
	}
	return tea.Batch(textarea.Blink, m.waitUpdates(), m.waitNotices(), m.waitVerification())
}
func (m Model) waitUpdates() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.backend.Updates:
			return refreshMsg{}
		case <-m.ctx.Done():
			return nil
		}
	}
}
func (m Model) waitNotices() tea.Cmd {
	return func() tea.Msg {
		select {
		case n := <-m.backend.Notices:
			return noticeMsg(n)
		case <-m.ctx.Done():
			return nil
		}
	}
}
func (m Model) waitVerification() tea.Cmd {
	return func() tea.Msg {
		select {
		case n := <-m.backend.Verifications:
			return verifyMsg(n)
		case <-m.ctx.Done():
			return nil
		}
	}
}
func (m *Model) reload() {
	rooms, e := m.db.Rooms()
	if e != nil {
		m.fail(e)
		return
	}
	m.rooms = rooms
	present := false
	for _, r := range rooms {
		if r.ID == m.current {
			present = true
			break
		}
	}
	if !present {
		m.drafts[m.current] = m.input.Value()
		m.current = ""
		m.selected = ""
		m.reply = ""
		m.thread = ""
		m.edit = ""
		m.scroll = 0
		if len(rooms) > 0 {
			m.current = rooms[0].ID
		}
		m.input.SetValue(m.drafts[m.current])
		_ = m.db.Put("selected_room", m.current)
	}
	if m.current == "" {
		m.messages = nil
		m.rendered = nil
		return
	}
	messages, e := m.db.Messages(m.current, m.limit)
	if e != nil {
		m.fail(e)
		return
	}
	if m.thread != "" {
		filtered := messages[:0]
		for _, msg := range messages {
			if msg.ID == m.thread || msg.ThreadRoot == m.thread {
				filtered = append(filtered, msg)
			}
		}
		messages = filtered
		if root, e := m.db.Message(m.thread); e == nil {
			found := false
			for _, msg := range messages {
				if msg.ID == root.ID {
					found = true
				}
			}
			if !found {
				messages = append([]store.Message{root}, messages...)
			}
		}
	}
	pending, e := m.db.Pending()
	if e != nil {
		m.fail(e)
		return
	}
	for _, p := range pending {
		if p.RoomID == m.current && (m.thread == "" || p.Content.GetRelatesTo().GetThreadParent().String() == m.thread) {
			messages = append(messages, store.Message{ID: p.Txn, RoomID: p.RoomID, Sender: m.userID(), Body: p.Content.Body, Kind: string(p.Content.MsgType), TS: p.TS, Pending: true, Error: p.Error})
		}
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].TS == messages[j].TS {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].TS < messages[j].TS
	})
	m.messages = messages
	m.names = map[string]string{}
	for _, msg := range messages {
		m.names[msg.Sender] = m.db.MemberName(m.current, msg.Sender)
	}
	// Preserve the top event while new content is appended above a scrolled viewport.
	anchor, anchorOffset := "", 0
	if m.scroll > 0 {
		top := max(0, len(m.rendered)-m.timelineHeight()-m.scroll)
		for id, span := range m.spans {
			if span[0] <= top && span[1] > top {
				anchor = id
				anchorOffset = top - span[0]
				break
			}
		}
	}
	m.renderTimeline()
	if anchor != "" {
		if span, ok := m.spans[anchor]; ok {
			m.scroll = max(0, len(m.rendered)-m.timelineHeight()-span[0]-anchorOffset)
		}
	}
	m.clampScroll()
}
func (m *Model) fail(e error) { m.status = e.Error(); m.statusError = true }
func (m Model) userID() string {
	if m.demo {
		return "@you:weft.local"
	}
	return m.backend.Client.UserID.String()
}
func (m Model) room() store.Room {
	for _, r := range m.rooms {
		if r.ID == m.current {
			return r
		}
	}
	return store.Room{ID: m.current}
}
func (m Model) sidebarWidth() int {
	if !m.settings.ShowSidebar || m.width < 76 {
		return 0
	}
	return min(29, max(23, m.width/4))
}
func (m Model) mainWidth() int      { return max(16, m.width-m.sidebarWidth()) }
func (m Model) timelineHeight() int { return max(1, m.height-11-m.completionHeight()) }
func (m *Model) clampScroll() {
	m.scroll = min(max(0, m.scroll), max(0, len(m.rendered)-m.timelineHeight()))
}
func (m *Model) switchRoom(room string) {
	if room == m.current {
		return
	}
	m.drafts[m.current] = m.input.Value()
	m.viUndo = nil
	m.viRedo = nil
	m.viPending = ""
	m.viCount = 0
	m.current = room
	_ = m.db.Put("selected_room", room)
	m.selected = ""
	m.reply = ""
	m.thread = ""
	m.edit = ""
	m.scroll = 0
	m.limit = m.settings.HistoryLimit
	m.input.SetValue(m.drafts[room])
	m.reload()
}
func (m *Model) nextRoom(delta int) {
	if len(m.rooms) == 0 {
		return
	}
	index := 0
	for i, r := range m.rooms {
		if r.ID == m.current {
			index = i
			break
		}
	}
	index = (index + delta + len(m.rooms)) % len(m.rooms)
	m.switchRoom(m.rooms[index].ID)
}
func (m *Model) selectMessage(delta int) {
	if len(m.messages) == 0 {
		return
	}
	index := len(m.messages)
	for i, msg := range m.messages {
		if msg.ID == m.selected {
			index = i
			break
		}
	}
	index = min(len(m.messages)-1, max(0, index+delta))
	m.selected = m.messages[index].ID
	m.renderTimeline()
	span := m.spans[m.selected]
	top := len(m.rendered) - m.timelineHeight() - m.scroll
	if span[0] < top {
		m.scroll = len(m.rendered) - m.timelineHeight() - span[0]
	} else if span[1] > top+m.timelineHeight() {
		m.scroll = len(m.rendered) - span[1]
	}
	m.clampScroll()
}
func (m *Model) openPrompt(mode string) {
	m.mode = mode
	m.prompt.Reset()
	m.prompt.EchoMode = textinput.EchoNormal
	m.prompt.Placeholder = ""
	m.promptIndex = 0
	m.input.Blur()
	m.prompt.Focus()
	if mode == "recover" || mode == "recover-passphrase" || mode == "setup-keys" {
		m.prompt.EchoMode = textinput.EchoPassword
		m.prompt.EchoCharacter = '•'
		m.prompt.Placeholder = "Recovery key (hidden)"
		if mode == "setup-keys" {
			m.prompt.Placeholder = "Account password (hidden)"
		}
		if mode == "recover-passphrase" {
			m.prompt.Placeholder = "Recovery passphrase (hidden)"
		}
	}
}
func (m *Model) closePrompt() {
	m.mode = ""
	m.prompt.Reset()
	m.prompt.Blur()
	if m.settings.KeyboardMode != "vi" || m.viInsert || m.input.Value() != "" {
		m.input.Focus()
	}
	m.details = ""
	m.search = nil
}
func (m Model) filteredRooms() []store.Room {
	q := strings.ToLower(m.prompt.Value())
	var rooms []store.Room
	for _, r := range m.rooms {
		if strings.Contains(strings.ToLower(r.Title()+" "+r.ID), q) {
			rooms = append(rooms, r)
		}
	}
	return rooms
}
func (m *Model) history() tea.Cmd {
	if m.historyBusy || m.current == "" {
		return nil
	}
	m.limit = min(10000, m.limit+m.settings.HistoryPageSize)
	m.reload()
	if m.demo {
		return nil
	}
	m.historyBusy = true
	room := m.current
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		return resultMsg{history: true, err: m.backend.History(ctx, room), text: "Earlier messages loaded"}
	}
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.FocusMsg:
		m.focused = true
		return m, m.autoRead()
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.input.SetWidth(max(8, m.mainWidth()-4))
		m.prompt.SetWidth(max(10, m.width-12))
		m.renderTimeline()
		m.clampScroll()
		return m, nil
	case refreshMsg:
		m.reload()
		return m, tea.Batch(m.waitUpdates(), m.autoRead())
	case noticeMsg:
		if v.Text == "Connected" || v.Text == "Connecting…" || strings.HasPrefix(v.Text, "Reconnecting:") || strings.HasPrefix(v.Text, "Sync stopped:") {
			m.connection = v.Text
		} else {
			m.status = v.Text
			m.statusError = v.Error
		}
		return m, m.waitNotices()
	case verifyMsg:
		verification := matrix.Verification(v)
		m.verification = &verification
		if v.Stage == "requested" || v.Stage == "sas" {
			m.mode = "verification"
			m.input.Blur()
		} else if v.Stage == "done" || v.Stage == "cancelled" {
			m.closePrompt()
			m.status = "Verification " + v.Stage
			if v.Stage == "cancelled" {
				m.status += " · " + v.SAS
			}
		}
		return m, m.waitVerification()
	case readResultMsg:
		if v.err != nil {
			delete(m.lastRead, v.room)
		}
		return m, nil
	case resultMsg:
		if !v.history {
			m.busy = false
		}
		if v.history {
			m.historyBusy = false
		}
		if v.err != nil {
			m.fail(v.err)
		} else {
			if v.text != "" {
				m.status = v.text
				m.statusError = false
			}
			if v.room != "" {
				m.switchRoom(v.room)
			}
			if v.details != "" {
				m.mode = "details"
				m.details = v.details
				m.input.Blur()
			}
		}
		m.reload()
		return m, nil
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.mode != "" {
			return m.updateModal(v)
		}

		if m.settings.KeyboardMode == "vi" {
			if key == "esc" && m.viInsert {
				m.viInsert = false
				m.viPending = ""
				m.viCount = 0
				if m.input.Value() == "" {
					m.input.Blur()
				} else {
					m.input.Focus()
					m.viMove("h", 1)
				}
				return m, nil
			}
			if !m.viInsert {
				if cmd, handled := m.updateVi(key); handled {
					return m, cmd
				}
				if len(v.Text) > 0 || key == "enter" || key == "tab" {
					return m, nil
				}
			}
		}
		if matches := m.commandMatches(); len(matches) > 0 {
			switch key {
			case "up", "down":
				delta := 1
				if key == "up" {
					delta = -1
				}
				m.completionIndex = (m.completionIndex + delta + len(matches)) % len(matches)
				return m, nil
			case "tab", "enter":
				m.input.SetValue("/" + matches[m.completionIndex%len(matches)] + " ")
				m.input.CursorEnd()
				m.completionIndex = 0
				return m, nil
			case "esc":
				m.completionDismissed = m.input.Value()
				return m, nil
			}
		}
		switch key {
		case "ctrl+q":
			return m, tea.Quit
		case "ctrl+k":
			m.openPrompt("rooms")
			return m, textinput.Blink
		case "alt+up", "ctrl+p":
			m.nextRoom(-1)
			return m, m.autoRead()
		case "alt+down", "ctrl+n":
			m.nextRoom(1)
			return m, m.autoRead()
		case "ctrl+up":
			m.selectMessage(-1)
			return m, nil
		case "ctrl+down":
			m.selectMessage(1)
			return m, nil
		case "pgup":
			m.scroll += max(1, m.timelineHeight()-2)
			m.clampScroll()
			if m.scroll >= max(0, len(m.rendered)-m.timelineHeight()-5) {
				cmd := m.history()
				return m, cmd
			}
			return m, nil
		case "pgdown":
			m.scroll -= max(1, m.timelineHeight()-2)
			m.clampScroll()
			return m, nil
		case "ctrl+end":
			m.scroll = 0
			m.selected = ""
			m.renderTimeline()
			return m, nil
		case "esc":
			m.reply = ""
			m.edit = ""
			m.selected = ""
			if m.thread != "" {
				m.thread = ""
				m.reload()
			} else {
				m.renderTimeline()
			}
			return m, nil
		case "f1":
			m.mode = "help"
			m.input.Blur()
			return m, nil
		case "alt+enter", "shift+enter":
			m.input.InsertRune('\n')
			return m, nil
		case "enter":
			line := strings.TrimSpace(m.input.Value())
			if line == "" {
				return m, nil
			}
			m.input.Reset()
			if strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "//") {
				cmd := m.command(line)
				return m, cmd
			}
			if strings.HasPrefix(line, "//") {
				line = line[1:]
			}
			if m.demo {
				m.status = "Offline demo: sign in to send real messages"
				return m, nil
			}
			if e := m.backend.Text(m.current, line, false, m.reply, m.thread, m.edit); e != nil {
				m.fail(e)
				m.input.SetValue(line)
			} else {
				m.reply = ""
				m.edit = ""
				m.scroll = 0
				m.reload()
			}
			return m, nil
		}
		if m.settings.TypingNotifications && !m.demo && m.current != "" && time.Since(m.lastTyping) > 5*time.Second && len(v.Text) > 0 {
			m.lastTyping = time.Now()
			room := m.current
			go func() {
				ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
				defer cancel()
				_, _ = m.backend.Client.UserTyping(ctx, idRoom(room), true, 7*time.Second)
			}()
		}
	case tea.MouseWheelMsg:
		switch v.String() {
		case "wheelup":
			m.scroll += m.settings.MouseScrollLines
		case "wheeldown":
			m.scroll -= m.settings.MouseScrollLines
		}
		m.clampScroll()
		return m, nil
	}
	var cmd tea.Cmd
	if m.mode != "" {
		if m.mode == "rooms" || m.mode == "search" || m.mode == "recover" || m.mode == "recover-passphrase" || m.mode == "setup-keys" {
			m.prompt, cmd = m.prompt.Update(msg)
		}
		return m, cmd
	}
	if m.settings.KeyboardMode == "vi" && !m.viInsert {
		return m, nil
	}
	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.completionIndex = 0
		m.completionDismissed = ""
	}
	return m, cmd
}
func (m Model) updateModal(v tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := v.String()
	if key == "esc" {
		m.closePrompt()
		return m, nil
	}
	if m.mode == "help" || m.mode == "details" {
		if key == "up" {
			m.promptIndex = max(0, m.promptIndex-1)
		}
		if key == "down" {
			m.promptIndex++
		}
		if key == "pgup" {
			m.promptIndex = max(0, m.promptIndex-10)
		}
		if key == "pgdown" {
			m.promptIndex += 10
		}
		if key == "enter" || key == "q" {
			m.closePrompt()
		}
		return m, nil
	}
	if m.mode == "verification" {
		if m.demo || m.verification == nil {
			return m, nil
		}
		txn := m.verification.Txn
		if key == "a" && m.verification.Stage == "requested" {
			m.closePrompt()
			return m, m.run("Verification accepted", func(ctx context.Context) error { return m.backend.AcceptVerification(ctx, txn) })
		}
		if key == "y" && m.verification.Stage == "sas" {
			m.closePrompt()
			return m, m.run("Verification confirmed", func(ctx context.Context) error { return m.backend.ConfirmVerification(ctx, txn) })
		}
		if key == "n" {
			m.closePrompt()
			return m, m.run("Verification cancelled", func(ctx context.Context) error {
				return m.backend.CancelVerification(ctx, txn, "m.mismatched_sas", "User rejected verification")
			})
		}
		return m, nil
	}
	if key == "up" {
		m.promptIndex = max(0, m.promptIndex-1)
		return m, nil
	}
	if key == "down" {
		m.promptIndex++
		return m, nil
	}
	if key == "enter" {
		switch m.mode {
		case "rooms":
			rooms := m.filteredRooms()
			if len(rooms) > 0 {
				m.switchRoom(rooms[min(m.promptIndex, len(rooms)-1)].ID)
			}
			m.closePrompt()
			return m, m.autoRead()
		case "setup-keys":
			password := m.prompt.Value()
			m.closePrompt()
			if password == "" {
				return m, nil
			}
			if m.busy {
				m.status = "An operation is still running"
				return m, nil
			}
			m.busy = true
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
				defer cancel()
				recovery, e := m.backend.SetupKeys(ctx, password)
				if recovery == "" {
					return resultMsg{err: e}
				}
				details := "Save this recovery key in your password manager.\nIt unlocks your encrypted history on a new device.\n\n" + recovery + "\n\nThis key is not saved in weft’s session file."
				if e != nil {
					details += "\n\nSetup did not finish: " + e.Error()
				} else {
					details += "\n\nEncryption identity and key backup are ready."
				}
				return resultMsg{details: details, text: "Store your recovery key safely"}
			}
		case "recover", "recover-passphrase":
			secret := m.prompt.Value()
			passphrase := m.mode == "recover-passphrase"
			m.closePrompt()
			if secret == "" {
				return m, nil
			}
			return m, m.run("Recovery completed", func(ctx context.Context) error { return m.backend.Recover(ctx, secret, passphrase) })
		case "search":
			query := m.prompt.Value()
			results, e := m.db.Search(m.current, query)
			if e != nil {
				m.fail(e)
				return m, nil
			}
			m.search = results
			m.mode = "search-results"
			m.promptIndex = 0
			return m, nil
		case "search-results":
			if len(m.search) > 0 {
				selected := m.search[min(m.promptIndex, len(m.search)-1)]
				m.thread = ""
				m.limit = 10000
				m.selected = selected.ID
				m.reload()
				if span, ok := m.spans[selected.ID]; ok {
					m.scroll = max(0, len(m.rendered)-m.timelineHeight()-span[0])
					m.clampScroll()
				}
			}
			m.closePrompt()
			return m, nil
		}
	}
	if m.mode == "search-results" {
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(v)
	if key != "up" && key != "down" {
		m.promptIndex = 0
	}
	return m, cmd
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || r == 0x202e || r == 0x202d || r == 0x2066 || r == 0x2067 || r == 0x2068 || r == 0x2069 {
			return -1
		}
		return r
	}, s)
}
func oneLine(s string) string  { return strings.ReplaceAll(clean(s), "\n", " ") }
func (m Model) String() string { return fmt.Sprintf("weft %s", m.current) }

type readResultMsg struct {
	room string
	err  error
}

func (m *Model) autoRead() tea.Cmd {
	if !m.settings.ReadReceipts || m.demo || !m.focused || m.mode != "" || m.scroll > 0 || m.current == "" || m.room().Membership != "join" {
		return nil
	}
	target := ""
	for i := len(m.messages) - 1; i >= 0; i-- {
		if !m.messages[i].Pending {
			target = m.messages[i].ID
			break
		}
	}
	if target == "" || m.lastRead[m.current] == target {
		return nil
	}
	room := m.current
	m.lastRead[room] = target
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		return readResultMsg{room: room, err: m.backend.MarkRead(ctx, room, target)}
	}
}
