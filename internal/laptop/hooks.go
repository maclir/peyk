package laptop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var peykPing = regexp.MustCompile(`\bpeyk"?\s+ping\b`)

// pingEvents are the hooks that report a turn starting and ending. A profile
// missing either leaves a prompt typed at the terminal unseen.
var pingEvents = []string{"UserPromptSubmit", "Stop"}

type hookSettings struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// PingsPeyk reports whether a Claude Code profile's settings run `peyk ping`
// when a turn starts and ends. An unreadable file counts as not pinging.
func PingsPeyk(claudeDir string) bool {
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		return false
	}
	var s hookSettings
	if json.Unmarshal(data, &s) != nil {
		return false
	}
	for _, event := range pingEvents {
		if !pings(s, event) {
			return false
		}
	}
	return true
}

func pings(s hookSettings, event string) bool {
	for _, m := range s.Hooks[event] {
		for _, h := range m.Hooks {
			if peykPing.MatchString(h.Command) {
				return true
			}
		}
	}
	return false
}

// profileOf returns the Claude Code profile that holds a transcript directory.
func profileOf(t string) string { return filepath.Dir(filepath.Dir(t)) }

// SettingsModel returns the default model configured in a Claude Code
// profile's settings.json, or "" when none is set or the file cannot be read.
func SettingsModel(claudeDir string) string {
	if claudeDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s.Model)
}
