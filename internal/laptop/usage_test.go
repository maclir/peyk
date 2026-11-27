package laptop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

func TestSaveUsageRoundTripsAndLeavesAnUnchangedFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage", "claude.json")
	if u, err := LoadUsage(path); err != nil || u != nil {
		t.Fatalf("LoadUsage before any save = %v, %v; want nil, nil", u, err)
	}
	u := &session.Usage{FiveHour: &session.Window{Used: 23.5, ResetsAt: time.Unix(1738425600, 0).UTC()}}
	if err := SaveUsage(path, u); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := SaveUsage(path, u); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(old) {
		t.Fatal("saving the same usage rewrote the file")
	}
	got, err := LoadUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SevenDay != nil || got.FiveHour.Used != 23.5 || !got.FiveHour.ResetsAt.Equal(u.FiveHour.ResetsAt) {
		t.Fatalf("LoadUsage = %+v, want %+v", got, u)
	}
}

func TestLoadAllUsageMergesEachToolsFile(t *testing.T) {
	dir := t.TempDir()
	if u, err := LoadAllUsage(dir); err != nil || u != nil {
		t.Fatalf("LoadAllUsage before any save = %v, %v; want nil, nil", u, err)
	}
	reset := time.Unix(1738425600, 0).UTC()
	if err := SaveUsage(filepath.Join(dir, AntigravityUsageFile), &session.Usage{Antigravity: []session.Pool{{Name: "Gemini", SevenDay: &session.Window{Used: 6, ResetsAt: reset}}}}); err != nil {
		t.Fatal(err)
	}
	u, err := LoadAllUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour != nil || len(u.Antigravity) != 1 || u.Antigravity[0].SevenDay.Used != 6 {
		t.Fatalf("LoadAllUsage with only Antigravity = %+v", u)
	}
	if err := SaveUsage(filepath.Join(dir, ClaudeUsageFile), &session.Usage{FiveHour: &session.Window{Used: 23.5, ResetsAt: reset}}); err != nil {
		t.Fatal(err)
	}
	if u, err = LoadAllUsage(dir); err != nil {
		t.Fatal(err)
	}
	if u.FiveHour == nil || u.FiveHour.Used != 23.5 || len(u.Antigravity) != 1 {
		t.Fatalf("LoadAllUsage with both = %+v", u)
	}
}
