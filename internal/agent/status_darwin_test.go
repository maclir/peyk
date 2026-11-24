package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusWatcherPingsOnlyWhenAStatusChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "s1")
	write := func(status string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pings := make(chan struct{}, 1)
	w := NewStatusWatcher(pings)
	defer w.Close()
	if err := w.Set(context.Background(), []string{dir}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	write("running")
	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("no ping when the session's directory and status appeared")
	}

	write("running")
	select {
	case <-pings:
		t.Fatal("rewriting the same status pinged")
	case <-time.After(1500 * time.Millisecond):
	}

	write("idle")
	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("no ping when the status changed")
	}
}
