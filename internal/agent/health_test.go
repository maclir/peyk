package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHealthIsWrittenWholeAndRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peyk", "agent.json")
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if err := WriteHealth(path, Health{PID: 42, StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"pid":42,"started_at":"2026-11-16T09:00:00Z","workers":0}` {
		t.Fatalf("before a refresh = %s", got)
	}
	if err := WriteHealth(path, Health{PID: 42, StartedAt: start, RefreshedAt: start.Add(time.Minute), Workers: 3}); err != nil {
		t.Fatal(err)
	}
	var h Health
	b, _ = os.ReadFile(path)
	if err := json.Unmarshal(b, &h); err != nil || h.Workers != 3 || !h.RefreshedAt.Equal(start.Add(time.Minute)) {
		t.Fatalf("after a refresh = %s (%v)", b, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
	if err := RemoveHealth(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHealth(path); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}
