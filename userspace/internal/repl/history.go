package repl

import (
	"os"
	"path/filepath"
	"strings"
)

// History persists CLI input lines to ~/.config/sd-cli/history (cap 500, mode 0600).
// Loaded at startup so reverse-search (Ctrl-R) and up-arrow recall prior
// sessions — operator convenience for long engagements.

const (
	historyCap  = 500
	historyFile = "history"
	historyDir  = "sd-cli"
)

func historyPath() (string, error) {
	home, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, historyDir, historyFile), nil
}

// LoadHistory reads the prior session history. Missing file is not an error
// (returns empty slice).
func LoadHistory() ([]string, error) {
	p, err := historyPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// SaveHistory caps and writes the history file. The caller passes the full
// in-memory history slice (newest at end).
func SaveHistory(lines []string) error {
	if len(lines) > historyCap {
		lines = lines[len(lines)-historyCap:]
	}
	p, err := historyPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
