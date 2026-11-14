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
	if last, err := git("log", "-1", "--format="+commitFormat); err == nil {
		if c, ok := parseCommit(last); ok {
			g.Last = &c
		}
	}
	// A branch with commits and nowhere to push them is unpushed work, not a
	// clean state: count what a first push would send.
	unpushed := []string{"HEAD", "--not", "--remotes"}
	if upstream, err := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		g.Upstream = upstream
		unpushed = []string{"@{u}..HEAD"}
	} else if g.Last == nil {
		return g
	}
	if n, err := git(append([]string{"rev-list", "--count"}, unpushed...)...); err == nil {
		g.Unpushed, _ = strconv.Atoi(n)
	}
	if g.Unpushed > 0 {
		args := append([]string{"log", "-n", strconv.Itoa(session.MaxPending), "--format=" + commitFormat}, unpushed...)
		if out, err := git(args...); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if c, ok := parseCommit(line); ok {
					g.Pending = append(g.Pending, c)
				}
			}
		}
	}
	return g
}

const commitFormat = "%h%x00%s%x00%cI"

func parseCommit(line string) (session.Commit, bool) {
	parts := strings.SplitN(line, "\x00", 3)
	if len(parts) != 3 {
		return session.Commit{}, false
	}
	at, _ := time.Parse(time.RFC3339, parts[2])
	return session.Commit{Hash: parts[0], Subject: parts[1], At: at}, true
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
