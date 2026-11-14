package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEditWatcherPingsOnFileEditsButNotGitInternals(t *testing.T) {
	// FSEvents reports canonical paths; /var is a symlink on macOS.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	pings := make(chan struct{}, 1)
	w := NewEditWatcher(pings)
	defer w.Close()
	if err := w.Set(context.Background(), []string{dir}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pings:
		t.Fatal("a change inside .git woke the edit watcher")
	case <-time.After(1500 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("no ping after editing a file")
	}

	if err := w.Set(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestInGitDir(t *testing.T) {
	for path, want := range map[string]bool{
		"Users/me/repo/.git/index":     true,
		"Users/me/repo/.git":           true,
		"Users/me/repo/src/main.go":    false,
		"Users/me/repo/.github/ci.yml": false,
	} {
		if got := inGitDir(path); got != want {
			t.Errorf("inGitDir(%q) = %v", path, got)
		}
	}
}
