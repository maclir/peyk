package laptop

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maclir/peyk/internal/session"
)

// Checkout is the git checkout an instance runs from. It updates by
// fast-forwarding to the branch it follows, never by merging, so local work
// is never mixed into an update.
type Checkout struct {
	Dir string
	Run Runner
}

func (c Checkout) git(ctx context.Context, args ...string) (string, error) {
	out, err := c.Run(ctx, c.Dir, "git", args...)
	return strings.TrimSpace(string(out)), err
}

// Fetch asks the remote for commits the checkout does not have.
func (c Checkout) Fetch(ctx context.Context) error {
	_, err := c.git(ctx, "fetch", "--quiet")
	return err
}

// Version compares HEAD with its upstream as last fetched. It does not
// fetch, so it is cheap enough to run on every refresh.
func (c Checkout) Version(ctx context.Context) (session.Version, error) {
	var v session.Version
	var err error
	if v.Commit, err = c.git(ctx, "rev-parse", "--short", "HEAD"); err != nil {
		return v, err
	}
	behind, err := c.git(ctx, "rev-list", "--count", "HEAD..@{upstream}")
	if err != nil {
		return v, err
	}
	if v.Behind, err = strconv.Atoi(behind); err != nil {
		return v, err
	}
	if p, err := c.git(ctx, "rev-parse", "--git-path", "FETCH_HEAD"); err == nil {
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.Dir, p)
		}
		if fi, err := os.Stat(p); err == nil {
			v.CheckedAt = fi.ModTime()
		}
	}
	return v, nil
}

// Update fast-forwards to the fetched upstream and returns the paths that
// changed.
func (c Checkout) Update(ctx context.Context) ([]string, error) {
	before, err := c.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if _, err := c.git(ctx, "merge", "--ff-only", "--quiet", "@{upstream}"); err != nil {
		return nil, err
	}
	out, err := c.git(ctx, "diff", "--name-only", before, "HEAD")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
