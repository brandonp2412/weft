package tui

import "strings"

var slashCommands = strings.Fields(`accept back cancel confirm create devices discard dm download embed edit help history image invite join leave me members name q quit react read recover recover-passphrase redact reply retry rooms search setup-keys thread topic upload verify whoami`)

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
	matches := m.commandMatches()
	if len(matches) == 0 {
		return 0
	}
	return min(len(matches), 5, max(0, m.height-13)) + 1
}

func (m Model) completionRows() []string {
	matches := m.commandMatches()
	height := m.completionHeight()
	if height == 0 {
		return nil
	}
	index := m.completionIndex % len(matches)
	start := max(0, index-(height-2))
	rows := []string{dimStyle.Render(fit("  ↑/↓ choose · Tab/Enter complete · Esc dismiss", m.mainWidth()))}
	for i := start; i < min(len(matches), start+height-1); i++ {
		text := "  /" + matches[i]
		style := dimStyle
		if i == index {
			text = "› /" + matches[i]
			style = accentStyle.Bold(true)
		}
		rows = append(rows, style.Render(fit(text, m.mainWidth())))
	}
	return rows
}
