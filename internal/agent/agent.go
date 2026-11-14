// Package agent keeps the published session state in step with the laptop.
// It does work only when told something changed, plus one slow heartbeat.
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

type Agent struct {
	Collect Collector
	Store   store.Store
	Now     func() time.Time
	// Debounce coalesces the burst of hook pings one agent turn produces.
	Debounce time.Duration
	// Heartbeat refreshes everything and marks the laptop seen. It catches what
	// no hook reports, such as a push made in a terminal.
	Heartbeat time.Duration
	Log       *log.Logger

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

// Run publishes until ctx ends, then marks the laptop offline.
func (a *Agent) Run(ctx context.Context, pings <-chan struct{}) error {
	if err := a.Store.SetLaptop(ctx, store.Laptop{Online: true, SeenAt: a.Now()}); err != nil {
		return err
	}
	a.refresh(ctx)
	heartbeat := time.NewTicker(a.Heartbeat)
	defer heartbeat.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			// ctx is already cancelled; the goodbye needs a context of its own.
			bye, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return a.Store.SetLaptop(bye, store.Laptop{Online: false, SeenAt: a.Now()})
		case <-pings:
			if debounce == nil {
				debounce = time.After(a.Debounce)
			}
		case <-debounce:
			debounce = nil
			a.refresh(ctx)
		case <-heartbeat.C:
			a.refresh(ctx)
			if err := a.Store.SetLaptop(ctx, store.Laptop{Online: true, SeenAt: a.Now()}); err != nil {
				a.Log.Printf("heartbeat: %v", err)
			}
		}
	}
}

func (a *Agent) refresh(ctx context.Context) {
	if err := a.Refresh(ctx); err != nil && ctx.Err() == nil {
		a.Log.Printf("refresh: %v", err)
	}
}
