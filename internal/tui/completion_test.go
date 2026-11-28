package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"weft/internal/store"
)

func TestCommandCompletion(t *testing.T) {
	m := demoModel(t)
	for _, value := range []string{"hello /re", "//", "/reply text", "/unknown", "/re\n"} {
		m.input.SetValue(value)
		if len(m.commandMatches()) != 0 {
			t.Fatalf("unexpected matches for %q", value)
		}
	}
	m.input.SetValue("/re")
	matches := m.commandMatches()
	if len(matches) < 2 {
		t.Fatal("missing prefix matches")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if got := m.input.Value(); got != "/"+matches[1]+" " {
		t.Fatalf("completion: %q", got)
	}
	if m.mode != "" {
		t.Fatal("completion executed a command")
	}
	m.input.SetValue("/help")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if len(m.commandMatches()) != 0 {
		t.Fatal("escape did not dismiss")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.(Model).mode != "help" {
		t.Fatal("dismissed command did not execute")
	}
}

func TestCompletionLayout(t *testing.T) {
	for _, size := range [][2]int{{36, 14}, {60, 20}, {100, 30}} {
		m := demoModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(Model)
		m.input.SetValue("/")
		m.completionIndex = len(m.commandMatches()) - 1
		rows := strings.Split(m.View().Content, "\n")
		if len(rows) > size[1] {
			t.Fatalf("%v: height %d", size, len(rows))
		}
		for _, row := range rows {
			if ansi.StringWidth(row) > size[0] {
				t.Fatalf("%v: row too wide", size)
			}
		}
		if !strings.Contains(ansi.Strip(m.View().Content), "/whoami") {
			t.Fatal("selected completion not visible")
		}
	}
}

func TestDownloadCompletion(t *testing.T) {
	m := demoModel(t)
	m.messages = []store.Message{
		{Kind: "m.file", Body: "Report.pdf"},
		{Kind: "m.text", Body: "ordinary message"},
		{Kind: "m.image", Body: "photo.png"},
		{Kind: "m.file", Body: "pending.zip", Pending: true},
		{Kind: "m.file", Body: "deleted.zip", Redacted: true},
	}
	m.input.SetValue("/download ")
	matches := m.completions()
	if len(matches) != 2 || matches[0].label != "#3 photo.png" || matches[1].label != "#1 Report.pdf" {
		t.Fatalf("unexpected attachment suggestions: %+v", matches)
	}
	for input, want := range map[string]string{
		"/download ":    "/download #3 ",
		"/download #1":  "/download #1 ",
		"/download PDF": "/download #1 ",
	} {
		m.input.SetValue(input)
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		completed := updated.(Model)
		if got := completed.input.Value(); got != want || cmd != nil || len(completed.completions()) != 0 {
			t.Fatalf("completion for %q: got %q, want %q ready for a save path", input, got, want)
		}
	}
}
