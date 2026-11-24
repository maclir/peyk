package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// StatusWatcher pings when aoe records a new status for a session: the
// prompt that starts a turn, the turn's end, or a question. aoe's hook
// rewrites the same status before every tool call, so only a changed status
// pings. It watches the directories' parents, because aoe creates a
// session's directory on its first hook.
type StatusWatcher struct {
	pings  chan<- struct{}
	files  *EditWatcher
	events chan struct{}
	done   chan struct{}

	mu   sync.Mutex
	last map[string]string
}

func NewStatusWatcher(pings chan<- struct{}) *StatusWatcher {
	events := make(chan struct{}, 1)
	w := &StatusWatcher{pings: pings, files: NewEditWatcher(events), events: events, done: make(chan struct{}), last: map[string]string{}}
	go w.forward()
	return w
}

// Set follows exactly these session hook directories.
func (w *StatusWatcher) Set(ctx context.Context, dirs []string) error {
	w.mu.Lock()
	last := map[string]string{}
	parents := map[string]bool{}
	for _, d := range dirs {
		// FSEvents reports canonical paths; /tmp is a symlink on macOS.
		if p, err := filepath.EvalSymlinks(filepath.Dir(d)); err == nil {
			d = filepath.Join(p, filepath.Base(d))
		}
		parents[filepath.Dir(d)] = true
		if s, ok := w.last[d]; ok {
			last[d] = s
		} else {
			last[d] = readStatus(d)
		}
	}
	w.last = last
	w.mu.Unlock()
	watch := make([]string, 0, len(parents))
	for p := range parents {
		watch = append(watch, p)
	}
	return w.files.Set(ctx, watch)
}

func (w *StatusWatcher) forward() {
	for {
		select {
		case <-w.done:
			return
		case <-w.events:
			if w.changed() {
				select {
				case w.pings <- struct{}{}:
				default:
				}
			}
		}
	}
}

// changed rereads every followed status and reports whether one moved.
func (w *StatusWatcher) changed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	moved := false
	for d, was := range w.last {
		if now := readStatus(d); now != was {
			w.last[d] = now
			moved = true
		}
	}
	return moved
}

func readStatus(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "status"))
	return string(b)
}

func (w *StatusWatcher) Close() {
	w.files.Close()
	close(w.done)
}
