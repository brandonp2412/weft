package tui

import (
	"strconv"
	"strings"
)

var slashCommands = strings.Fields(`accept back cancel confirm create devices discard dm download embed edit help history image invite join leave me members name pin q quit react read readall recover recover-passphrase redact reply retry rooms search setup-keys thread topic unpin upload verify whoami`)

func (m Model) commandMatches() []string {
	value := m.input.Value()
	if m.mode != "" || value == m.completionDismissed || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\r\n") {
		return nil
	}
	prefix := strings.TrimPrefix(value, "/")
	var matches []string
	for _, command := range slashCommands {
		if strings.HasPrefix(command, prefix) {
			matches = append(matches, command)
		}
	}
	return matches
}

func (m Model) completionHeight() int {
	return m.completionHeightFor(len(m.completions()))
}

type completion struct {
	value string
	label string
}

func (m Model) completions() []completion {
	var matches []completion
	for _, command := range m.commandMatches() {
		value := "/" + command
		matches = append(matches, completion{value + " ", value})
	}
	value := m.input.Value()
	prefix, ok := strings.CutPrefix(value, "/download ")
	if m.mode != "" || value == m.completionDismissed || !ok || strings.ContainsAny(prefix, " \t\r\n") {
		return matches
	}
	prefix = strings.ToLower(prefix)
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.Pending || msg.Redacted {
			continue
		}
		switch msg.Kind {
		case "m.file", "m.image", "m.audio", "m.video", "m.sticker":
			ref := "#" + strconv.Itoa(i+1)
			if strings.HasPrefix(ref, prefix) || strings.Contains(strings.ToLower(msg.Body), prefix) {
				matches = append(matches, completion{"/download " + ref + " ", ref + " " + clean(msg.Body)})
			}
		}
	}
	return matches
}

func (m Model) completionHeightFor(count int) int {
	if count == 0 {
		return 0
	}
	return min(count, 5, max(0, m.height-13)) + 1
}

func (m Model) completionRows() []string {
	matches := m.completions()
	height := m.completionHeightFor(len(matches))
	if height == 0 {
		return nil
	}
	index := m.completionIndex % len(matches)
	start := max(0, index-(height-2))
	hint := "  ↑/↓ choose · Tab/Enter complete · Esc dismiss"
	if strings.HasPrefix(m.input.Value(), "/download ") {
		hint = "  ↑/↓ choose · Tab complete · Esc dismiss"
	}
	rows := []string{dimStyle.Render(fit(hint, m.mainWidth()))}
	for i := start; i < min(len(matches), start+height-1); i++ {
		text := "  " + matches[i].label
		style := dimStyle
		if i == index {
			text = "› " + matches[i].label
			style = accentStyle.Bold(true)
		}
		rows = append(rows, style.Render(fit(text, m.mainWidth())))
	}
	return rows
}
