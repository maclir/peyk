package laptop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSettings(t *testing.T, claudeDir, settings string) {
	t.Helper()
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

const pingHook = `[{"hooks":[{"type":"command","command":"sh -c '[ -n \"$AOE_INSTANCE_ID\" ] || exit 0; exec \"$HOME/peyk/bin/peyk\" ping'"}]}]`

func TestPingsPeykNeedsAPingOnTurnStartAndEnd(t *testing.T) {
	for name, tt := range map[string]struct {
		settings string
		want     bool
	}{
		"both":        {`{"hooks":{"UserPromptSubmit":` + pingHook + `,"Stop":` + pingHook + `}}`, true},
		"no stop":     {`{"hooks":{"UserPromptSubmit":` + pingHook + `}}`, false},
		"other hooks": {`{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"peyk usage"}]}],"Stop":` + pingHook + `}}`, false},
		"no hooks":    {`{}`, false},
		"unparseable": {`{`, false},
		"no settings": {"", false},
	} {
		dir := filepath.Join(t.TempDir(), ".claude")
		if tt.settings != "" {
			writeSettings(t, dir, tt.settings)
		}
		if got := PingsPeyk(dir); got != tt.want {
			t.Errorf("%s: PingsPeyk = %v", name, got)
		}
	}
}

func TestSnapshotFollowsAoeHooksOnlyWithoutPeykPing(t *testing.T) {
	home := t.TempDir()
	hooked, plain := filepath.Join(home, ".claude-a"), filepath.Join(home, ".claude-b")
	writeSettings(t, hooked, `{"hooks":{"UserPromptSubmit":`+pingHook+`,"Stop":`+pingHook+`}}`)
	writeSettings(t, plain, `{}`)
	writeTranscript(t, hooked, "/h", "th", time.Now(), human("2026-11-14T09:00:00Z", `"hi"`))
	writeTranscript(t, plain, "/p", "tp", time.Now(), human("2026-11-14T09:00:00Z", `"hi"`))
	notGit := func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	c := Collector{Run: notGit, ClaudeDirs: []string{hooked, plain}, AoeHooks: "/hooks", Now: time.Now}
	if s := c.Snapshot(context.Background(), AoeSession{ID: "h", Path: "/h", AgentSessionID: "th"}); s.AoeHooks != "" {
		t.Fatalf("a profile that pings peyk followed %q", s.AoeHooks)
	}
	if s := c.Snapshot(context.Background(), AoeSession{ID: "p", Path: "/p", AgentSessionID: "tp"}); s.AoeHooks != "/hooks/p" {
		t.Fatalf("a profile without peyk ping followed %q", s.AoeHooks)
	}
}
