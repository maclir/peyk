package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"
)

// EditWatcher pings when files change in a working tree, which catches edits
// made in a terminal or editor that no hook reports. It uses one FSEvents
// stream for all trees, so watching a large repository costs no file handles.
// Changes under .git are left to GitWatcher, and the collector's own git
// reads touch nothing else, so a refresh does not wake it.
type EditWatcher struct {
	pings chan<- struct{}
	mu    sync.Mutex
	paths []string
	es    *fsevents.EventStream
}

func NewEditWatcher(pings chan<- struct{}) *EditWatcher { return &EditWatcher{pings: pings} }

// Set watches exactly these trees, restarting the stream when they change.
func (w *EditWatcher) Set(_ context.Context, dirs []string) error {
	paths := append([]string(nil), dirs...)
	sort.Strings(paths)
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.Join(paths, "\x00") == strings.Join(w.paths, "\x00") {
		return nil
	}
	if w.es != nil {
		w.es.Stop()
		w.es = nil
	}
	w.paths = paths
	if len(paths) == 0 {
		return nil
	}
	es := &fsevents.EventStream{Paths: paths, Latency: 500 * time.Millisecond, Flags: fsevents.FileEvents | fsevents.NoDefer}
	if err := es.Start(); err != nil {
		return fmt.Errorf("watch working trees: %w", err)
	}
	w.es = es
	go func(events chan []fsevents.Event) {
		for batch := range events {
			for _, ev := range batch {
				// A file edit has its own event; a directory event alone,
				// such as FSEvents replaying a tree's creation, is not one.
				if inGitDir(ev.Path) || ev.Flags&fsevents.ItemIsDir != 0 {
					continue
				}
				select {
				case w.pings <- struct{}{}:
				default:
				}
				break
			}
		}
	}(es.Events)
	return nil
}

func (w *EditWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.es != nil {
		w.es.Stop()
		w.es = nil
	}
}

func inGitDir(path string) bool {
	return strings.Contains("/"+path+"/", "/.git/")
}
