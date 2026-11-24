package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"slices"
	"weft/internal/matrix"
)

// droppedFile recognizes the local paths commonly pasted by terminal drag and
// drop. It never evaluates shell expressions or treats ordinary prose as a path.
func droppedFile(text string) (string, bool) {
	text = strings.TrimSpace(text)
	candidates := []string{text}
	if strings.HasPrefix(text, "file://") {
		if u, err := url.Parse(text); err == nil && (u.Host == "" || u.Host == "localhost") && u.RawQuery == "" && u.Fragment == "" {
			candidates = []string{u.Path}
		} else {
			return "", false
		}
	} else if decoded, ok := unquoteDroppedPath(text); ok {
		candidates = append(candidates, decoded)
	}
	// Foot escapes apostrophes and may wrap the entire path in single quotes.
	if strings.Contains(text, "\\'") {
		path := text
		if strings.HasPrefix(path, "'/") && strings.HasSuffix(path, "'") {
			path = path[1 : len(path)-1]
		}
		candidates = append(candidates, strings.ReplaceAll(path, "\\'", "'"))
	}
	for _, path := range candidates {
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../") {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

func unquoteDroppedPath(text string) (string, bool) {
	var out strings.Builder
	var quote rune
	escaped := false
	for _, ch := range text {
		if escaped {
			out.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				out.WriteRune(ch)
			}
		} else {
			switch ch {
			case '\'', '"':
				quote = ch
			case ' ', '\t', '\n', '\r':
				return "", false
			default:
				out.WriteRune(ch)
			}
		}
	}
	return out.String(), quote == 0 && !escaped
}

type stagedAttachment struct {
	path     string
	name     string
	mimeType string
	data     []byte
}

type clipboardResult struct {
	request uint64
	text    string
	image   *stagedAttachment
	err     error
}

func (m *Model) pasteClipboard() tea.Cmd {
	m.clipboardRequest++
	request, ctx, allowImages := m.clipboardRequest, m.ctx, m.mode == ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result := readClipboard(ctx, allowImages)
		result.request = request
		return result
	}
}

func readClipboard(ctx context.Context, allowImages bool) clipboardResult {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		text, err := clipboard.ReadAll()
		return clipboardResult{text: text, err: err}
	}
	types, err := readWaylandClipboard(ctx, 64<<10, "--list-types")
	if err != nil {
		return clipboardResult{err: err}
	}
	offered := strings.Split(strings.TrimSpace(string(types)), "\n")
	for _, mimeType := range offered {
		if allowImages && strings.HasPrefix(mimeType, "image/") {
			data, err := readWaylandClipboard(ctx, matrix.MaxAttachment, "--no-newline", "--type", mimeType)
			if err != nil {
				return clipboardResult{err: err}
			}
			if len(data) == 0 {
				return clipboardResult{err: errors.New("clipboard image is empty")}
			}
			extensions, _ := mime.ExtensionsByType(mimeType)
			name := "clipboard-image"
			if len(extensions) != 0 {
				name += extensions[0]
			}
			return clipboardResult{image: &stagedAttachment{name: name, mimeType: mimeType, data: data}}
		}
	}
	for _, mimeType := range []string{"text/uri-list", "text/plain;charset=utf-8", "UTF8_STRING", "text/plain", "TEXT", "STRING"} {
		if slices.Contains(offered, mimeType) {
			data, err := readWaylandClipboard(ctx, matrix.MaxAttachment, "--no-newline", "--type", mimeType)
			return clipboardResult{text: string(data), err: err}
		}
	}
	return clipboardResult{err: errors.New("clipboard has no supported text or image data")}
}

func readWaylandClipboard(ctx context.Context, limit int64, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wl-paste", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("clipboard: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(out, limit+1))
	if err != nil || int64(len(data)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("clipboard data exceeds the size limit")
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("could not read Wayland clipboard: %w", err)
	}
	return data, nil
}

func isTextPaste(text string) bool {
	if !utf8.ValidString(text) || strings.HasPrefix(text, "PNG\r\n\x1a\n") {
		return false
	}
	for _, ch := range text {
		if unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t' {
			return false
		}
	}
	return true
}
