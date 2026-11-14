package laptop

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// RepoState reads a working directory's branch, last commit, push state and
// uncommitted changes. It returns nil when dir is not inside a git repository.
func RepoState(ctx context.Context, run Runner, dir string) *session.Git {
	git := func(args ...string) (string, error) {
		out, err := run(ctx, dir, "git", args...)
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		return nil
	}
	g := &session.Git{}
	g.Branch, _ = git("branch", "--show-current")
	if status, err := git("status", "--porcelain"); err == nil && status != "" {
		g.Dirty = len(strings.Split(status, "\n"))
	}
	if last, err := git("log", "-1", "--format=%h%x00%s%x00%cI"); err == nil && last != "" {
		parts := strings.SplitN(last, "\x00", 3)
		if len(parts) == 3 {
			at, _ := time.Parse(time.RFC3339, parts[2])
			g.Last = &session.Commit{Hash: parts[0], Subject: parts[1], At: at}
		}
	}
	if upstream, err := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		g.Upstream = upstream
		if n, err := git("rev-list", "--count", "@{u}..HEAD"); err == nil {
			g.Unpushed, _ = strconv.Atoi(n)
		}
	} else if g.Last != nil {
		// A branch with commits and nowhere to push them is unpushed work, not
		// a clean state: count what a first push would send.
		if n, err := git("rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
			g.Unpushed, _ = strconv.Atoi(n)
		}
	}
	return g
}

// Push pushes the checked-out branch to its upstream, or to origin under the
// same name when it has none.
func Push(ctx context.Context, run Runner, dir string) error {
	if _, err := run(ctx, dir, "git", "rev-parse", "--abbrev-ref", "@{u}"); err == nil {
		_, err := run(ctx, dir, "git", "push")
		return err
	}
	_, err := run(ctx, dir, "git", "push", "--set-upstream", "origin", "HEAD")
	return err
}
