package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	ink         = lipgloss.Color("#D9E5E3")
	muted       = lipgloss.Color("#80928F")
	teal        = lipgloss.Color("#78DBC2")
	gold        = lipgloss.Color("#EBCB8B")
	red         = lipgloss.Color("#F29B9B")
	bg          = lipgloss.Color("#101A1B")
	panel       = lipgloss.Color("#152223")
	line        = lipgloss.Color("#2A3D3D")
	codeBG      = lipgloss.Color("#203031")
	quoteColor  = lipgloss.Color("#A6B8B3")
	bodyStyle   = lipgloss.NewStyle().Foreground(ink)
	dimStyle    = lipgloss.NewStyle().Foreground(muted)
	accentStyle = lipgloss.NewStyle().Foreground(teal)
	quoteStyle  = lipgloss.NewStyle().Foreground(quoteColor).Italic(true)
	codeStyle   = lipgloss.NewStyle().Foreground(teal).Background(codeBG)
	linkStyle   = lipgloss.NewStyle().Foreground(teal).Underline(true)
)

var (
	linkPattern     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	urlPattern      = regexp.MustCompile(`https?://[^\s<>()]+`)
	codePattern     = regexp.MustCompile("`([^`]+)`")
	strongPattern   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	emphasisPattern = regexp.MustCompile(`\*([^*]+)\*|_([^_]+)_`)
	strikePattern   = regexp.MustCompile(`~~([^~]+)~~`)
)

func clip(s string, width int) string { return ansi.Truncate(s, max(0, width), "…") }
func fit(s string, width int) string {
	s = clip(s, width)
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}
func (m Model) timeLayout() string {
	if m.settings.TimeFormat == "12h" {
		return "3:04 PM"
	}
	return "15:04"
}

type timelineTile struct {
	row  int
	tile sixelTile
}

func (m *Model) renderTimeline() {
	m.rendered = nil
	m.timelineTiles = nil
	m.spans = map[string][2]int{}
	width := max(10, m.mainWidth()-6)
	date := ""
	for _, msg := range m.messages {
		start := len(m.rendered)
		t := time.UnixMilli(msg.TS)
		day := t.Format("Monday, 2 January 2006")
		if day != date {
			m.rendered = append(m.rendered, "", dimStyle.Render("  ── "+day+" ──"), "")
			date = day
		}
		name := oneLine(m.names[msg.Sender])
		if name == "" {
			name = oneLine(msg.Sender)
		}
		badge := ""
		if msg.Pending {
			badge = "  queued"
			if msg.Error != "" {
				badge = "  retrying"
			}
		}
		if msg.Edited {
			badge += "  edited"
		}
		prefix := "  "
		if msg.ID == m.selected {
			prefix = "▎ "
		}
		heading := fmt.Sprintf("%s%s  %s%s", prefix, name, t.Format(m.timeLayout()), badge)
		style := accentStyle
		if msg.Sender == m.userID() {
			style = lipgloss.NewStyle().Foreground(gold)
		}
		if msg.ID == m.selected {
			style = style.Bold(true)
		}
		m.rendered = append(m.rendered, clip(style.Render(heading), m.mainWidth()-2))
		if msg.ReplyTo != "" {
			label := "reply to " + msg.ReplyTo
			for _, original := range m.messages {
				if original.ID == msg.ReplyTo {
					label = oneLine(original.Body)
					break
				}
			}
			m.rendered = append(m.rendered, accentStyle.Render("    ↳ ")+quoteStyle.Render(clip(label, width)))
		}
		text := clean(msg.Body)
		if msg.Kind == "m.emote" {
			text = "* " + name + " " + text
		}
		switch msg.Kind {
		case "m.image", "m.file", "m.audio", "m.video", "m.sticker":
			text = "↧ " + text + "  [attachment · /download selected path]"
		}
		if msg.Redacted {
			text = "[message deleted]"
		}
		m.rendered = append(m.rendered, renderMessageBody(text, width)...)
		if m.settings.ImagePreviews && !msg.Redacted && msg.Kind == "m.image" {
			thumb := m.previews[msg.ID]
			if tile := m.imageTile(thumb, width, min(17, max(1, m.height-13))); tile.image != nil {
				m.timelineTiles = append(m.timelineTiles, timelineTile{len(m.rendered), tile})
				m.rendered = append(m.rendered, tile.blankLines()...)
			} else {
				m.rendered = append(m.rendered, previewLines(thumb, width, previewRows)...)
			}
		}
		if msg.Reactions != "" {
			m.rendered = append(m.rendered, accentStyle.Render(clip("    "+oneLine(msg.Reactions), width+4)))
		}
		m.rendered = append(m.rendered, "")
		m.spans[msg.ID] = [2]int{start, len(m.rendered)}
	}
}

func renderMessageBody(text string, width int) []string {
	var rows []string
	inCode := false
	for _, raw := range strings.Split(clean(text), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode {
			rows = appendWrapped(rows, codeStyle, "    "+raw, width)
			continue
		}
		quote := strings.HasPrefix(trimmed, ">")
		if quote {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			for _, row := range strings.Split(ansi.Wrap(formatInline(trimmed), max(1, width-2), "      "), "\n") {
				rows = append(rows, quoteStyle.Render("    │ "+row))
			}
			continue
		}
		rows = appendWrapped(rows, bodyStyle, "    "+formatInline(raw), width)
	}
	return rows
}

func appendWrapped(rows []string, style lipgloss.Style, text string, width int) []string {
	return append(rows, wrapStyled(style.Render(text), width)...)
}

func wrapStyled(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, max(1, width), "    "), "\n")
}

func formatInline(text string) string {
	text = linkPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := linkPattern.FindStringSubmatch(match)
		return linkStyle.Render(parts[1])
	})
	text = urlPattern.ReplaceAllStringFunc(text, func(match string) string {
		return linkStyle.Render(match)
	})
	text = codePattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := codePattern.FindStringSubmatch(match)
		return codeStyle.Render(parts[1])
	})
	text = strongPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := strongPattern.FindStringSubmatch(match)
		content := parts[1]
		if content == "" {
			content = parts[2]
		}
		return lipgloss.NewStyle().Foreground(ink).Bold(true).Render(content)
	})
	text = emphasisPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := emphasisPattern.FindStringSubmatch(match)
		content := parts[1]
		if content == "" {
			content = parts[2]
		}
		return lipgloss.NewStyle().Foreground(ink).Italic(true).Render(content)
	})
	text = strikePattern.ReplaceAllStringFunc(text, func(match string) string {
		return lipgloss.NewStyle().Foreground(muted).Strikethrough(true).Render(strikePattern.FindStringSubmatch(match)[1])
	})
	return text
}
func (m Model) sidebar() string {
	w := m.sidebarWidth()
	if w == 0 {
		return ""
	}
	var rows []string
	rows = append(rows, accentStyle.Bold(true).Render("  weft"), dimStyle.Render(clip("  MATRIX IN THE TERMINAL", w-2)), "", dimStyle.Render("  ROOMS"))
	start, end := m.sidebarRoomRange()
	for _, r := range m.rooms[start:end] {
		icon := "  "
		if r.Pinned {
			icon = "📌"
		}
		if r.Membership == "invite" {
			icon = "+ "
		}
		suffix := ""
		if r.Unread > 0 {
			suffix = " ●"
		}
		text := " " + icon + " " + oneLine(r.Title())
		text = fit(text, w-2-ansi.StringWidth(suffix)) + suffix
		s := lipgloss.NewStyle().Foreground(muted)
		if r.ID == m.current {
			s = s.Foreground(teal).Background(line).Bold(true)
		} else if r.Unread > 0 {
			s = s.Foreground(ink).Bold(true)
		}
		rows = append(rows, s.Render(fit(text, w-1)))
	}
	for len(rows) < m.height-3 {
		rows = append(rows, "")
	}
	rows = append(rows, dimStyle.Render(clip("  "+oneLine(m.userID()), w-2)), dimStyle.Render("  ^K switch rooms"))
	return lipgloss.NewStyle().Width(w - 1).Height(m.height - 1).Background(panel).BorderRight(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(line).Render(strings.Join(rows, "\n"))
}
func (m Model) mainView() (string, []sixelPlacement) {
	w := m.mainWidth()
	r := m.room()
	title := oneLine(r.Title())
	if title == "" {
		title = "Your conversations"
	}
	badge := ""
	if r.Membership == "invite" {
		badge = "  invitation · /join to accept"
	}
	header := accentStyle.Bold(true).Render(clip("  "+title, w-2)) + dimStyle.Render(clip(badge, max(0, w-ansi.StringWidth(title)-4)))
	topic := oneLine(r.Topic)
	if m.thread != "" {
		topic = "Thread · Esc returns to the room"
	}
	if topic == "" {
		topic = r.ID
	}
	rows := []string{fit(header, w), dimStyle.Render(fit("  "+clip(topic, w-4), w)), dimStyle.Render(strings.Repeat("─", w))}
	height := m.timelineHeight()
	start, end := m.visibleTimeline()
	timeline := m.rendered[start:end]
	var images []sixelPlacement
	for _, positioned := range m.timelineTiles {
		top := max(start, positioned.row)
		bottom := min(end, positioned.row+positioned.tile.rows())
		if bottom > top {
			images = append(images, sixelPlacement{
				tile: positioned.tile, x: m.sidebarWidth() + 4, y: len(rows) + top - start,
				firstRow: top - positioned.row, rows: bottom - top,
			})
		}
	}
	if len(m.messages) == 0 {
		welcome := []string{"", "  A little space to talk.", "", "  Your messages will appear here.", "  Ctrl+K switches rooms. /join joins a room.", "  /create name starts an encrypted room.", "", "  Press F1 for the keyboard guide."}
		if r.Membership == "invite" {
			welcome = []string{"", "  You’ve been invited.", "", "  Type /join to accept this invitation.", "  Type /leave confirm to decline."}
		}
		for _, l := range welcome {
			if len(timeline) < height {
				timeline = append(timeline, dimStyle.Render(clip(l, w)))
			}
		}
	}
	for _, l := range timeline {
		rows = append(rows, fit(l, w))
	}
	for len(rows) < height+3 {
		rows = append(rows, strings.Repeat(" ", w))
	}
	rows = append(rows, m.completionRows()...)
	contextLine := ""
	if m.reply != "" {
		contextLine = "Replying · Esc to cancel"
	}
	if m.edit != "" {
		contextLine = "Editing message · Esc to cancel"
	}
	if m.scroll > 0 {
		contextLine = fmt.Sprintf("↑ History · %d lines above latest · Ctrl+End to return", m.scroll)
	}
	if m.historyBusy {
		contextLine = "Loading earlier messages…"
	}
	if contextLine == "" && !m.demo {
		if users := m.backend.Typing(m.current); len(users) > 0 {
			contextLine = strings.Join(users, ", ") + " typing…"
		}
	}
	rows = append(rows, dimStyle.Render(fit("  "+clip(contextLine, w-4), w)), dimStyle.Render(strings.Repeat("─", w)))
	rows = append(rows, strings.Split(m.input.View(), "\n")...)
	if tile := m.composerTile(); tile.image != nil {
		images = append(images, sixelPlacement{tile: tile, x: m.sidebarWidth() + 4, y: len(rows), rows: tile.rows()})
		rows = append(rows, tile.blankLines()...)
	} else {
		for _, row := range m.composerPreview() {
			rows = append(rows, fit(row, w))
		}
	}
	statusStyle := dimStyle
	if m.statusError {
		statusStyle = lipgloss.NewStyle().Foreground(red)
	}
	status := m.status
	if m.attachment != nil {
		attachment := "Image ready · Enter uploads · Esc discards"
		if m.attachment.path != "" {
			attachment = "File ready · Enter uploads · Esc discards"
		}
		if status == "" {
			status = attachment
		} else {
			status += " · " + attachment
		}
	}
	if m.settings.KeyboardMode == "vi" {
		label := "VI NORMAL"
		if m.viInsert {
			label = "VI INSERT"
		}
		status = label + " · " + status
	}
	rows = append(rows, statusStyle.Render(fit("  "+clip(oneLine(status), w-4), w)))
	connection := m.connection
	if m.busy {
		connection = "Working… · " + connection
	}
	footer := accentStyle.Render("  ● ") + dimStyle.Render(clip(oneLine(connection), w-6))
	rows = append(rows, fit(footer, w))
	return lipgloss.NewStyle().Width(w).Height(m.height).Background(bg).Render(strings.Join(rows, "\n")), images
}
func (m Model) View() tea.View {
	var content string
	var images []sixelPlacement
	if m.width < 36 || m.height < 14 {
		content = "weft\nResize the terminal to at least 36 × 14.\nCtrl+C exits."
	} else if m.mode != "" {
		content = m.modalView()
	} else {
		var main string
		main, images = m.mainView()
		content = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebar(), main)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "weft"
	if m.graphics != nil {
		v.WindowTitle = m.graphics.frameTitle(images)
	}
	v.BackgroundColor = bg
	v.ForegroundColor = ink
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	return v
}
func (m Model) modalView() string {
	width := min(100, max(28, m.width-8))
	height := max(1, m.height-12)
	title := ""
	lines := []string{}
	footer := "Esc returns to chat"
	switch m.mode {
	case "quit":
		title = "Exit weft?"
		lines = []string{"Are you sure you want to exit? (y/n)"}
		footer = "y exit · n/Esc cancel"
	case "rooms":
		title = "Switch conversation"
		lines = append(lines, m.prompt.View(), "")
		rooms := m.filteredRooms()
		index := min(m.promptIndex, max(0, len(rooms)-1))
		start := max(0, index-height+4)
		for i := start; i < min(len(rooms), start+height-3); i++ {
			r := rooms[i]
			label := oneLine(r.Title())
			if r.Pinned {
				label = "📌 " + label
			}
			text := clip("  "+label+"  "+r.ID, width-6)
			if i == index {
				text = accentStyle.Bold(true).Render("› " + strings.TrimSpace(text))
			}
			lines = append(lines, text)
		}
		if len(rooms) == 0 {
			lines = append(lines, "  No matching conversations")
		}
		footer = "↑/↓ choose · Enter switch · Esc cancel"
	case "setup-keys":
		title = "Set up encryption for a new account"
		lines = []string{"Creates cross-signing and an encrypted server key backup.", "Existing encryption identities will never be replaced.", "", "Enter your account password to authorize setup:", m.prompt.View(), "", "A recovery key will appear once. Save it securely."}
		footer = "Enter set up · Esc cancel"
	case "recover", "recover-passphrase":
		title = "Restore encryption keys"
		lines = []string{"Unlock existing secret storage to verify this device", "and restore messages from your server key backup.", "", m.prompt.View(), "", "Your secret is hidden and is never saved to disk."}
		footer = "Enter restore · Esc cancel"
	case "search":
		title = "Search this conversation"
		lines = []string{"Search locally cached message text.", "", m.prompt.View()}
		footer = "Enter search · Esc cancel"
	case "search-results":
		title = fmt.Sprintf("Search · %d results", len(m.search))
		index := min(m.promptIndex, max(0, len(m.search)-1))
		start := max(0, index-height/2+1)
		for i := start; i < min(len(m.search), start+height/2); i++ {
			msg := m.search[i]
			prefix := "  "
			if i == index {
				prefix = "› "
			}
			lines = append(lines, accentStyle.Render(clip(prefix+oneLine(msg.Sender)+" · "+time.UnixMilli(msg.TS).Format("Jan 2 "+m.timeLayout()), width-6)), clip("  "+oneLine(msg.Body), width-6))
		}
		if len(m.search) == 0 {
			lines = []string{"No matches in cached history. Load older messages with PgUp."}
		}
		footer = "↑/↓ choose · Enter jump to message · Esc cancel"
	case "verification":
		title = "Verify this device"
		if m.verification != nil {
			v := m.verification
			switch v.Stage {
			case "requested":
				lines = []string{"Verification requested by " + oneLine(v.User), "Device: " + oneLine(v.Device), "", "Press a to accept, or n to reject."}
			case "sas":
				lines = []string{"Compare these symbols or numbers with the other device.", "", v.SAS, "", "Press y ONLY if they match, or n if they differ."}
			default:
				lines = []string{"Waiting for the other device…"}
			}
		}
		footer = "a accept · y symbols match · n reject · Esc hide"
	case "details":
		title = "Details"
		lines = strings.Split(clean(m.details), "\n")
	case "help":
		title = "weft · keyboard & commands"
		text := helpText
		if m.settings.KeyboardMode == "vi" {
			text = "Vi: empty draft j/k chats · gg/G first/last chat\nH/M/L select message at top/middle/bottom of view\nDraft: h/j/k/l move · w/b/e words · 0/^/$ line · gg/G draft ends\nEnter/i/a/I/A insert · q exit (y/n) · ZZ quit · o/O open line · Esc normal · counts: 3j, 2dd\nd/c/y + motion · dd/cc/yy line · D/C to end · x delete\np/P paste · u undo · Ctrl+R redo · : command (empty draft) · / search\n\n" + text
		}
		lines = strings.Split(text, "\n")
		footer = "Enter or Esc returns to chat"
	}
	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(l, width-6, ""), "\n")...)
	}
	// Details/help use a compact full-screen sheet. Long results remain scrollable.
	offset := max(0, m.promptIndex)
	if m.mode == "details" || m.mode == "help" {
		offset = min(offset, max(0, len(wrapped)-height))
		wrapped = wrapped[offset:]
	}
	if len(wrapped) > height {
		wrapped = wrapped[:height]
	}
	if m.mode == "rooms" {
		// Keep the input and footer stationary as filtering changes the result count.
		for len(wrapped) < height {
			wrapped = append(wrapped, "")
		}
	}
	box := lipgloss.NewStyle().Width(width).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(teal).Background(panel).Foreground(ink).Render(accentStyle.Bold(true).Render(title) + "\n\n" + strings.Join(wrapped, "\n") + "\n\n" + dimStyle.Render(clip(footer, width-6)))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

const helpText = `Enter send · Alt+Enter newline · Ctrl+K room picker
/ command suggestions · ↑/↓ choose · Tab/Enter complete · Esc dismiss
Alt+↑/↓ or Ctrl+P/N switch rooms · Ctrl+↑/↓ select message
PgUp/PgDown scroll · Ctrl+End latest · Ctrl+C quit
Esc cancel reply/edit or return from a thread

Messages: Ctrl+↑/↓ to select, then use selected or $eventID
/reply selected [text]    /edit selected [text]    /me action
/thread selected [text]   /back             /react selected emoji
/redact selected [reason]                  /read
/upload /path/to/file                /download selected /save/path
/image /path/to/image or mxc://server/media [caption]
/embed selected [caption]           Reuse an existing image
/search [query]    /history          /retry
/discard selected (cancel a queued send)    //text sends /text

Rooms
/pin /unpin                         Matrix favourite
/join #alias:server or !roomID        /leave confirm
/create room name (encrypted)        /dm @user:server
/invite @user:server                 /members
/topic new topic                    /name new name

Encryption
/verify [@user:server]  request verification on another device
/accept /confirm /cancel             /devices /whoami
/setup-keys           set up encryption on a fresh account
/recover              unlock with a recovery key (hidden)
/recover-passphrase   unlock with a recovery passphrase
/retry                retry decryption and queued sends

Search covers cached history. PgUp loads earlier messages.
Attachments up to 64 MiB; encrypted in encrypted rooms.
↑/↓ scroll this guide.`
