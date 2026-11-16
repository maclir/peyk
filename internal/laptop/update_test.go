package laptop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckoutSeesFetchedCommitsAndFastForwardsToThem(t *testing.T) {
	ctx := context.Background()
	upstream := gitRepo(t)
	commit(t, upstream, "README", "first")
	dir := filepath.Join(t.TempDir(), "clone")
	mustGit(t, upstream, "clone", "-q", upstream, dir)
	c := Checkout{Dir: dir, Run: Exec}

	v, err := c.Version(ctx)
	if err != nil || v.Behind != 0 || v.Commit == "" || !v.CheckedAt.IsZero() {
		t.Fatalf("fresh clone: %+v, %v", v, err)
	}
	if err := os.MkdirAll(filepath.Join(upstream, "menubar"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit(t, upstream, "menubar/app.swift", "menu")
	commit(t, upstream, "main.go", "agent")

	if v, _ := c.Version(ctx); v.Behind != 0 {
		t.Fatalf("version must not fetch: %+v", v)
	}
	if err := c.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	v, err = c.Version(ctx)
	if err != nil || v.Behind != 2 || v.CheckedAt.IsZero() {
		t.Fatalf("after fetch: %+v, %v", v, err)
	}
	changed, err := c.Update(ctx)
	if err != nil || strings.Join(changed, ",") != "main.go,menubar/app.swift" {
		t.Fatalf("update changed %v, %v", changed, err)
	}
	if v, _ := c.Version(ctx); v.Behind != 0 {
		t.Fatalf("after update: %+v", v)
	}
	if changed, err := c.Update(ctx); err != nil || len(changed) != 0 {
		t.Fatalf("second update changed %v, %v", changed, err)
	}
}

func TestCheckoutRefusesToMergeLocalCommits(t *testing.T) {
	ctx := context.Background()
	upstream := gitRepo(t)
	commit(t, upstream, "README", "first")
	dir := filepath.Join(t.TempDir(), "clone")
	mustGit(t, upstream, "clone", "-q", upstream, dir)
	for _, args := range [][]string{{"config", "user.email", "t@example.com"}, {"config", "user.name", "T"}, {"config", "commit.gpgsign", "false"}} {
		mustGit(t, dir, args...)
	}
	commit(t, upstream, "a", "upstream work")
	commit(t, dir, "b", "local work")
	c := Checkout{Dir: dir, Run: Exec}
	if err := c.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Update(ctx); err == nil {
		t.Fatal("diverged checkout was updated")
	}
}
