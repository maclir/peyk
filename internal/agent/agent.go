// Package agent keeps the published session state in step with the laptop.
// It does work only when told something changed.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

type Collector interface {
	All(context.Context) ([]session.Snapshot, error)
	Repos() ([]string, error)
	// Exchange rereads a session's latest reply and prompt.
	Exchange(session.Transcript) (reply, prompt *session.Message)
}

// logoFinder is a Collector that can also find a project's mark.
type logoFinder interface {
	Logo(dir string) string
}

// Watcher follows the working directories of the published sessions.
type Notifier interface {
	Notify(context.Context, []notify.Note) error
}

type Watcher interface {
	Set(ctx context.Context, workdirs []string) error
}

type Agent struct {
	Collect Collector
	Store   store.Store
	Now     func() time.Time
	// Watch, when set, is pointed at the sessions' directories after every refresh.
	Watch Watcher
	// Edits, when set, is pointed at the working trees of sessions that are
	// not working. A working agent edits constantly and its Stop hook reports
	// the result, so watching it would only refresh for nothing.
	Edits Watcher
	// Transcripts, when set, is pointed at the transcript directories of
	// working sessions, and pings Replies when one is written. No hook fires
	// when a working agent writes a reply in the middle of its turn, and a
	// full refresh per tool call would rerun every git command for it.
	Transcripts Watcher
	Replies     <-chan struct{}
	// Debounce coalesces the burst of pings one agent turn produces.
	Debounce time.Duration
	// Presence only rewrites seen_at. A laptop that sleeps cannot say so, and
	// this is how the phone tells a sleeping laptop from a running one. It
	// never refreshes sessions: a change nothing reported is a bug to fix.
	Presence time.Duration
	Log      *log.Logger

	// Commands, when set, lets the phone make the laptop act.
	Commands *Commands
	// Notify, when set, is told about sessions that start needing you or
	// finish a turn.
	Notify Notifier

	published map[string]map[string]string
	// The last published state, which commands act on.
	sessions map[string]session.Snapshot
	projects map[string]session.Project
}

// Refresh collects every session and project and writes only the documents
// whose content changed.
func (a *Agent) Refresh(ctx context.Context) error {
	snaps, err := a.Collect.All(ctx)
	if err != nil {
		return err
	}
	repos, err := a.Collect.Repos()
	if err != nil {
		return err
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
	if a.Edits != nil {
		var idle []string
		for _, s := range snaps {
			if s.State != session.Working && s.Git != nil {
				idle = append(idle, s.Path)
			}
		}
		if err := a.Edits.Set(ctx, idle); err != nil {
			return err
		}
	}
	if a.Transcripts != nil {
		var dirs []string
		for _, s := range snaps {
			if s.State == session.Working && s.Transcript.Dir != "" {
				dirs = append(dirs, s.Transcript.Dir)
			}
		}
		if err := a.Transcripts.Set(ctx, dirs); err != nil {
			return err
		}
	}
	prev := a.sessions
	sessions := map[string]any{}
	a.sessions = map[string]session.Snapshot{}
	for _, s := range snaps {
		sessions[s.ID] = s
		a.sessions[s.ID] = s
	}
	projects := map[string]any{}
	a.projects = map[string]session.Project{}
	logos, _ := a.Collect.(logoFinder)
	for _, p := range session.Projects(snaps, repos) {
		if logos != nil {
			p.Logo = logos.Logo(p.Path)
		}
		projects[ProjectID(p.Name)] = p
		a.projects[p.Name] = p
	}
	if err := a.sync(ctx, store.Sessions, sessions); err != nil {
		return err
	}
	if err := a.sync(ctx, store.Projects, projects); err != nil {
		return err
	}
	if err := a.finishTasks(ctx); err != nil {
		return err
	}
	if a.Notify == nil || prev == nil {
		return nil
	}
	if notes := notify.Changes(prev, a.sessions); len(notes) > 0 {
		return a.Notify.Notify(ctx, notes)
	}
	return nil
}

// RefreshReplies rereads the transcripts of working sessions and publishes
// the ones whose reply or prompt changed. Their state is left to Refresh.
func (a *Agent) RefreshReplies(ctx context.Context) error {
	if a.sessions == nil {
		return nil
	}
	docs := make(map[string]any, len(a.sessions))
	for id, s := range a.sessions {
		if s.State == session.Working {
			s.SetExchange(a.Collect.Exchange(s.Transcript))
			a.sessions[id] = s
		}
		docs[id] = s
	}
	return a.sync(ctx, store.Sessions, docs)
}

// finishTasks marks dispatched tasks done once their session has finished
// them. Tasks whose session is gone stay dispatched; deleting them is yours.
func (a *Agent) finishTasks(ctx context.Context) error {
	tr, ok := a.Store.(store.TaskReader)
	if !ok {
		return nil
	}
	tasks, err := tr.DispatchedTasks(ctx)
	if err != nil {
		return err
	}
	for id, t := range tasks {
		s, ok := a.sessions[t.SessionID]
		if !ok {
			continue
		}
		done, commit := session.Finished(s, t.DispatchedAt)
		if !done {
			continue
		}
		fields := map[string]any{"status": store.TaskDone, "done_at": a.Now()}
		if commit != "" {
			fields["commit"] = commit
		}
		if err := a.Store.Update(ctx, store.Tasks, id, fields); err != nil {
			return err
		}
	}
	return nil
}

// ProjectID is a project's document id: its name, which Firestore accepts
// once slashes are gone.
func ProjectID(name string) string { return strings.ReplaceAll(name, "/", "_") }

// sync makes a collection hold exactly docs, writing only what changed since
// the last sync. The first sync learns what is already there, so documents
// left by an earlier run are removed.
func (a *Agent) sync(ctx context.Context, collection string, docs map[string]any) error {
	if a.published == nil {
		a.published = map[string]map[string]string{}
	}
	published, ok := a.published[collection]
	if !ok {
		ids, err := a.Store.IDs(ctx, collection)
		if err != nil {
			return err
		}
		published = make(map[string]string, len(ids))
		for _, id := range ids {
			published[id] = ""
		}
		a.published[collection] = published
	}
	for id, v := range docs {
		key := contentKey(v)
		if published[id] == key {
			continue
		}
		if err := a.Store.Put(ctx, collection, id, v); err != nil {
			return err
		}
		published[id] = key
	}
	for id := range published {
		if _, ok := docs[id]; ok {
			continue
		}
		if err := a.Store.Delete(ctx, collection, id); err != nil {
			return err
		}
		delete(published, id)
	}
	return nil
}

func contentKey(v any) string {
	if s, ok := v.(session.Snapshot); ok {
		s.UpdatedAt = time.Time{}
		v = s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Run publishes until ctx ends or something fails. Either way it marks the
// laptop offline before returning, so the phone never shows stale state as live.
func (a *Agent) Run(ctx context.Context, pings <-chan struct{}, cmds <-chan store.Incoming) (err error) {
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
	var debounce, replies <-chan time.Time
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
		case <-a.Replies:
			if replies == nil {
				replies = time.After(a.Debounce)
			}
		case <-replies:
			replies = nil
			if err := a.RefreshReplies(ctx); err != nil {
				return err
			}
		case in := <-cmds:
			if a.Commands == nil {
				continue
			}
			if err := a.execute(ctx, in); err != nil {
				return err
			}
		case <-presence.C:
			if err := a.Store.SetLaptop(ctx, store.Laptop{Online: true, SeenAt: a.Now()}); err != nil {
				return err
			}
		}
	}
}
