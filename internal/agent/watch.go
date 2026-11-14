package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"

	"github.com/maclir/peyk/internal/laptop"
)

// GitWatcher pings when a repository's commits or branches move. That is
// what Claude Code hooks miss: a commit or push made in a terminal. It watches
// the HEAD reflog, which every commit, checkout and reset appends to, and the
// refs directories, where a commit, push or fetch renames a ref into place.
// Neither is written by the read-only git commands the collector runs, so its
// own reads cannot wake it.
type GitWatcher struct {
	Run     laptop.Runner
	w       *fsnotify.Watcher
	watched map[string]bool
}

func NewGitWatcher(run laptop.Runner) (*GitWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &GitWatcher{Run: run, w: w, watched: map[string]bool{}}, nil
}

// Forward turns file events into pings until ctx ends, and returns the first
// watcher error.
func (g *GitWatcher) Forward(ctx context.Context, pings chan<- struct{}) error {
	for {
		select {
		case <-ctx.Done():
			return g.w.Close()
		case ev, ok := <-g.w.Events:
			if !ok {
				return nil
			}
			if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && ev.Has(fsnotify.Create) {
				// A first push or a branch name with a slash adds directories,
				// often nested ones created before this event arrives.
				if err := g.addTree(ev.Name); err != nil {
					return err
				}
			}
			select {
			case pings <- struct{}{}:
			default:
			}
		case err, ok := <-g.w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("git watcher: %w", err)
		}
	}
}

// Set watches exactly the repositories containing these working directories.
// Directories outside a repository are skipped.
func (g *GitWatcher) Set(ctx context.Context, workdirs []string) error {
	want := map[string]bool{}
	for _, dir := range workdirs {
		paths, err := g.paths(ctx, dir)
		if err != nil {
			return err
		}
		for _, p := range paths {
			want[p] = true
		}
	}
	for p := range g.watched {
		if !want[p] {
			if err := g.w.Remove(p); err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "can't remove non-existent") {
				return fmt.Errorf("unwatch %s: %w", p, err)
			}
			delete(g.watched, p)
		}
	}
	for p := range want {
		if g.watched[p] {
			continue
		}
		if err := g.w.Add(p); err != nil {
			return fmt.Errorf("watch %s: %w", p, err)
		}
		g.watched[p] = true
	}
	return nil
}

func (g *GitWatcher) addTree(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || g.watched[p] {
			return nil
		}
		if err := g.w.Add(p); err != nil {
			return fmt.Errorf("watch %s: %w", p, err)
		}
		g.watched[p] = true
		return nil
	})
}

// Watched lists what is being watched, for tests.
func (g *GitWatcher) Watched() []string {
	out := make([]string, 0, len(g.watched))
	for p := range g.watched {
		out = append(out, p)
	}
	return out
}

func (g *GitWatcher) paths(ctx context.Context, workdir string) ([]string, error) {
	out, err := g.Run(ctx, workdir, "git", "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return nil, nil // not a repository
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil, fmt.Errorf("unexpected git rev-parse output in %s: %q", workdir, out)
	}
	gitDir, common := lines[0], lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(workdir, common)
	}
	var paths []string
	if _, err := os.Stat(filepath.Join(gitDir, "logs", "HEAD")); err == nil {
		paths = append(paths, filepath.Join(gitDir, "logs", "HEAD"))
	}
	refs := filepath.Join(common, "refs")
	if err := filepath.WalkDir(refs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list %s: %w", refs, err)
	}
	return paths, nil
}
