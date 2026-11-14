package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/laptop"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := laptop.Exec(context.Background(), dir, "git", args...); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
}

func expectPing(t *testing.T, pings <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-pings:
	case <-time.After(3 * time.Second):
		t.Fatalf("no ping after %s", what)
	}
}

func drain(pings <-chan struct{}) {
	time.Sleep(150 * time.Millisecond)
	for {
		select {
		case <-pings:
		default:
			return
		}
	}
}

func TestGitWatcherPingsOnTerminalCommitAndPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare", "-b", "main")
	repo := newRepo(t)
	commitFile(t, repo, "a")
	git(t, repo, "remote", "add", "origin", remote)

	w, err := NewGitWatcher(laptop.Exec)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Set(ctx, []string{repo, t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	pings := make(chan struct{}, 1)
	errs := make(chan error, 1)
	go func() { errs <- w.Forward(ctx, pings) }()

	commitFile(t, repo, "b")
	expectPing(t, pings, "commit")
	drain(pings)

	// The first push creates refs/remotes; the watcher has to follow it.
	git(t, repo, "push", "-q", "-u", "origin", "main")
	expectPing(t, pings, "first push")
	drain(pings)
	commitFile(t, repo, "c")
	drain(pings)
	git(t, repo, "push", "-q")
	expectPing(t, pings, "second push")
	drain(pings)

	// The collector's own reads must not wake it, or every refresh would
	// schedule the next one.
	for _, args := range [][]string{{"status", "--porcelain"}, {"log", "-1"}, {"rev-list", "--count", "@{u}..HEAD"}, {"branch", "--show-current"}} {
		git(t, repo, args...)
	}
	select {
	case <-pings:
		t.Fatal("read-only git commands woke the watcher")
	case <-time.After(300 * time.Millisecond):
	}

	if err := w.Set(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(w.Watched()); n != 0 {
		t.Fatalf("still watching %d paths", n)
	}
	cancel()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}
