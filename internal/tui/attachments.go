package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
