package matrix

import (
	"os"
	"path/filepath"
	"strings"
)

func downloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	data, err := os.ReadFile(filepath.Join(config, "user-dirs.dirs"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for line := range strings.Lines(string(data)) {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "XDG_DOWNLOAD_DIR=")
		if !ok || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			continue
		}
		path := value[1 : len(value)-1]
		unescape := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, `$`, "\\`", "`")
		if relative, ok := strings.CutPrefix(path, "$HOME/"); ok {
			return filepath.Join(home, unescape.Replace(relative)), nil
		}
		if filepath.IsAbs(path) {
			return unescape.Replace(path), nil
		}
	}
	return filepath.Join(home, "Downloads"), nil
}
