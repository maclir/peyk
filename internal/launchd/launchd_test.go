package launchd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlistDescribesAKeptAliveJob(t *testing.T) {
	j := Job{
		Label:     Agent,
		Program:   []string{"/usr/bin/make", "-s", "serve"},
		Dir:       "/code/peyk",
		Env:       map[string]string{"PATH": "/bin", "CLAUDE_CONFIG_DIR": "/h/.claude & co"},
		Log:       "/h/.peyk/agent.log",
		KeepAlive: true,
	}
	got := string(j.Plist())
	for _, want := range []string{
		"<string>com.peyk.agent</string>",
		"<string>/usr/bin/make</string>\n\t\t<string>-s</string>\n\t\t<string>serve</string>",
		"<key>WorkingDirectory</key>\n\t<string>/code/peyk</string>",
		"<key>CLAUDE_CONFIG_DIR</key>\n\t\t<string>/h/.claude &amp; co</string>\n\t\t<key>PATH</key>",
		"<key>StandardErrorPath</key>\n\t<string>/h/.peyk/agent.log</string>",
		"<key>KeepAlive</key>\n\t<true/>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist lacks %q:\n%s", want, got)
		}
	}
	if runtime.GOOS == "darwin" {
		path := filepath.Join(t.TempDir(), "job.plist")
		if err := os.WriteFile(path, j.Plist(), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %v\n%s", err, out)
		}
	}
}

func TestPlistOfAOneShotJobHasNoKeepAlive(t *testing.T) {
	got := string(Job{Label: Menu, Program: []string{"/app/Peyk"}}.Plist())
	if strings.Contains(got, "KeepAlive") || strings.Contains(got, "WorkingDirectory") || strings.Contains(got, "EnvironmentVariables") {
		t.Fatalf("plist = %s", got)
	}
}

func TestCaptureTakesOnlyListedSetVariables(t *testing.T) {
	env := map[string]string{"PATH": "/bin", "CLAUDE_CONFIG_DIR": "", "GOOGLE_CLOUD_PROJECT": "p", "GOPATH": "/go"}
	got := Capture(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if len(got) != 2 || got["PATH"] != "/bin" || got["GOPATH"] != "/go" {
		t.Fatalf("captured %v", got)
	}
}
