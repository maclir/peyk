// Package agent keeps the published session state in step with the laptop.
// It does work only when told something changed.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

type Collector interface {
	All(context.Context) ([]session.Snapshot, error)
}

// Watcher follows the working directories of the published sessions.
type Watcher interface {
	Set(ctx context.Context, workdirs []string) error
}

type Agent struct {
	Collect Collector
	Store   store.Store
	Now     func() time.Time
	// Watch, when set, is pointed at the sessions' directories after every refresh.
	Watch Watcher
	// Debounce coalesces the burst of pings one agent turn produces.
	Debounce time.Duration
	// Presence only rewrites seen_at. A laptop that sleeps cannot say so, and
	// this is how the phone tells a sleeping laptop from a running one. It
	// never refreshes sessions: a change nothing reported is a bug to fix.
	Presence time.Duration
	Log      *log.Logger

	published map[string]string
}

// Refresh collects every session and writes only the ones whose content changed.
func (a *Agent) Refresh(ctx context.Context) error {
	snaps, err := a.Collect.All(ctx)
	if err != nil {
		return err
	}
	if a.published == nil {
		ids, err := a.Store.SessionIDs(ctx)
		if err != nil {
			return err
		}
		a.published = make(map[string]string, len(ids))
		for _, id := range ids {
			a.published[id] = ""
		}
	}
	if a.Watch != nil {
		dirs := make([]string, len(snaps))
		for i, s := range snaps {
			dirs[i] = s.Path
		}
		if err := a.Watch.Set(ctx, dirs); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(snaps))
	for _, s := range snaps {
		seen[s.ID] = true
		key := contentKey(s)
		if a.published[s.ID] == key {
			continue
		}
		if err := a.Store.PutSession(ctx, s); err != nil {
			return err
		}
		a.published[s.ID] = key
	}
	for id := range a.published {
		if seen[id] {
			continue
		}
		if err := a.Store.DeleteSession(ctx, id); err != nil {
			return err
		}
		delete(a.published, id)
	}
	return nil
}

func contentKey(s session.Snapshot) string {
	s.UpdatedAt = time.Time{}
	b, _ := json.Marshal(s)
	return string(b)
}

// Run publishes until ctx ends or something fails. Either way it marks the
// laptop offline before returning, so the phone never shows stale state as live.
func (a *Agent) Run(ctx context.Context, pings <-chan struct{}) (err error) {
	defer func() {
		// ctx may be cancelled; the goodbye needs a context of its own.
		bye, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if offErr := a.Store.SetLaptop(bye, store.Laptop{Online: false, SeenAt: a.Now()}); err == nil {
			err = offErr
		}
	}()
	if err := a.Refresh(ctx); err != nil {
		return err
	}
	if err := a.Store.SetLaptop(ctx, store.Laptop{Online: true, SeenAt: a.Now()}); err != nil {
		return err
	}
	presence := time.NewTicker(a.Presence)
	defer presence.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pings:
			if debounce == nil {
				debounce = time.After(a.Debounce)
			}
		case <-debounce:
			debounce = nil
			if err := a.Refresh(ctx); err != nil {
				return err
			}
		case <-presence.C:
			if err := a.Store.SetLaptop(ctx, store.Laptop{Online: true, SeenAt: a.Now()}); err != nil {
				return err
			}
		}
	}
}
