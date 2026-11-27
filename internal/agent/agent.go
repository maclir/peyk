// Package agent keeps the published session state in step with the laptop.
// It does work only when told something changed.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

type Collector interface {
	All(context.Context) ([]session.Snapshot, error)
	Repos() ([]string, error)
	// Exchange rereads a session's latest reply and prompt.
	Exchange(session.Transcript) (reply, prompt *session.Message)
	// Transcribe fills in everything a snapshot reads from its transcript.
	Transcribe(*session.Snapshot)
}

// logoFinder is a Collector that can also find a project's mark.
type logoFinder interface {
	Logo(dir string) string
}

// Watcher follows the working directories of the published sessions.
type Notifier interface {
	Notify(context.Context, []notify.Note) error
}

// CIRuns looks up the workflow runs for a pushed commit; nil means none yet.
type CIRuns interface {
	Runs(ctx context.Context, repo, sha string) (*session.CI, error)
}

// PowerEvent is the laptop going to sleep, or waking fully.
type PowerEvent struct {
	Asleep bool
	// Done, on a sleep, lets the sleep proceed.
	Done func()
}

// powerRetry spaces attempts to go online after a wake, which can come before
// the network is back.
const powerRetry = 15 * time.Second

// HiddenProjects keeps the names of the projects hidden from the phone's
// picker across restarts.
type HiddenProjects interface {
	Load() ([]string, error)
	Save([]string) error
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
	// full refresh per tool call would rerun every git command for it. It
	// also follows sessions with an open question, because no hook fires
	// when the question is answered at the terminal either.
	Transcripts Watcher
	Replies     <-chan struct{}
	// Statuses, when set, is pointed at aoe's hook directories of sessions
	// whose Claude Code profile does not run `peyk ping`, and pings a full
	// refresh when aoe records a new status. A profile's own hook is faster
	// and is preferred wherever it runs.
	Statuses Watcher
	// Debounce coalesces the burst of pings one agent turn produces.
	Debounce time.Duration
	// Presence only rewrites seen_at. A laptop that sleeps cannot say so, and
	// this is how the phone tells a sleeping laptop from a running one. It
	// never refreshes sessions: a change nothing reported is a bug to fix.
	Presence time.Duration
	// Power, when set, reports sleep and wake, so the phone shows a closed
	// lid at once rather than after a missed presence.
	Power <-chan PowerEvent
	// Asleep, when set, reports a dark wake at start. launchd restarts a
	// failed agent during one, and Power reports only the full wake after it.
	Asleep func() bool
	Log    *log.Logger

	// Commands, when set, lets the phone make the laptop act.
	Commands *Commands
	// Hidden, when set, lets the phone hide projects from its picker.
	Hidden HiddenProjects
	hidden map[string]bool
	// Notify, when set, is told about sessions that start needing you or
	// finish a turn.
	Notify Notifier
	// Seen sends the sessions a device showed. Each one notified since it was
	// last seen gets a dismissal, so every device closes its notification,
	// including devices that never showed the session.
	Seen           <-chan string
	notified       map[string]bool
	notifiedLoaded bool
	// Checkout, when set, is the checkout the agent runs from: it publishes
	// the checkout's version after every refresh and lets the phone fetch
	// and update it.
	Checkout Checkout
	version  string
	// restart is set once an update has landed; Run then returns, and
	// launchd starts the agent again, which rebuilds it.
	restart bool
	// sessionsChanged is set by a command that added or replaced a session.
	sessionsChanged bool

	// Refreshed, when set, is told the sessions after every refresh that
	// published them.
	Refreshed func(at time.Time, snaps []session.Snapshot)
	// LaptopWritten, when set, is told every write of the laptop's status
	// and its error. The phone reads that status, so a failed write means
	// the phone shows the laptop as it was before, even though this process
	// is running.
	LaptopWritten func(l store.Laptop, err error)
	// CI, when set, follows the workflow runs of each session's pushed
	// commit. GitHub cannot tell the laptop a run moved on, so the agent
	// asks every CIPoll, and only while a run is unfinished or, for CIWait
	// after a push, not yet queued.
	CI     CIRuns
	CIPoll time.Duration
	CIWait time.Duration
	ciRuns map[string]*ciRun

	// History, when set, reads a session's whole conversation, and the agent
	// publishes it for each of HistoryRequests. Nothing is published
	// unless the phone asks, so an unread conversation costs nothing.
	History         func(session.Transcript) (entries []session.Entry, truncated bool)
	HistoryRequests <-chan store.HistoryRequest

	// Usage, when set, reads the subscription's rate limits as Claude Code's
	// status line last saved them, and the agent rereads it on UsageChanged.
	// It is published apart from a refresh: it changes with every reply, and
	// a refresh reruns every session's git commands.
	Usage        func() (*session.Usage, error)
	UsageChanged <-chan struct{}
	usageKey     string
	// UsageRead, when set, is told each changed usage, whether or not
	// publishing it succeeds, since the menu bar shows it without Firestore.
	UsageRead func(*session.Usage)

	// Actions, when set, reads the month's GitHub Actions usage. Billing has
	// no event, so the agent asks at start and after a followed run
	// finishes, which is when usage grows.
	Actions    func(context.Context) (*session.Actions, error)
	actionsDue bool
	actionsKey string
	// ActionsRead, when set, is told each changed Actions usage, whether or
	// not publishing it succeeds.
	ActionsRead func(*session.Actions)

	// Models, when set, lists the models Antigravity can run. It is read at
	// start: the list changes only with the account's plan or the CLI.
	Models func(context.Context) ([]session.Model, error)
	models map[string]bool

	// clears follows sessions cleared from the phone until aoe reports
	// their new conversation, keyed by session id.
	clears map[string]clearedTo

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
	a.followClears(snaps)
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
			if (s.State == session.Working || s.Question != nil) && s.Transcript.Dir != "" {
				dirs = append(dirs, s.Transcript.Dir)
			}
		}
		if err := a.Transcripts.Set(ctx, dirs); err != nil {
			return err
		}
	}
	if a.Statuses != nil {
		var dirs []string
		for _, s := range snaps {
			if s.AoeHooks != "" {
				dirs = append(dirs, s.AoeHooks)
			}
		}
		if err := a.Statuses.Set(ctx, dirs); err != nil {
			return err
		}
	}
	a.attachCI(ctx, snaps)
	prev := a.sessions
	a.sessions = map[string]session.Snapshot{}
	for _, s := range snaps {
		a.sessions[s.ID] = s
	}
	a.projects = map[string]session.Project{}
	logos, _ := a.Collect.(logoFinder)
	for _, p := range session.Projects(snaps, repos) {
		if logos != nil {
			p.Logo = logos.Logo(p.Path)
		}
		a.projects[p.Name] = p
	}
	if err := a.publishSessions(ctx); err != nil {
		return err
	}
	if err := a.publishProjects(ctx); err != nil {
		return err
	}
	if err := a.finishTasks(ctx); err != nil {
		return err
	}
	if conversationsChanged(prev, a.sessions) {
		if err := a.dropHeld(ctx); err != nil {
			return err
		}
	}
	if a.Refreshed != nil {
		a.Refreshed(a.Now(), snaps)
	}
	if err := a.publishVersion(ctx); err != nil {
		return err
	}
	if prev == nil {
		return nil
	}
	if err := a.notify(ctx, notify.Changes(prev, a.sessions)); err != nil {
		return err
	}
	return a.publishSessions(ctx)
}

func (a *Agent) notify(ctx context.Context, notes []notify.Note) error {
	if a.Notify == nil || len(notes) == 0 {
		return nil
	}
	if err := a.Notify.Notify(ctx, notes); err != nil {
		return err
	}
	if a.notified == nil {
		a.notified = map[string]bool{}
	}
	for _, n := range notes {
		a.notified[n.Session] = true
	}
	return nil
}

// seen dismisses a session's notifications everywhere and clears its badge.
// A failure only logs: a notification left open is no reason to stop the
// agent.
func (a *Agent) seen(ctx context.Context, id string) {
	if !a.notified[id] {
		return
	}
	delete(a.notified, id)
	if a.Notify != nil {
		if err := a.Notify.Notify(ctx, []notify.Note{notify.Dismissal(id)}); err != nil {
			a.Log.Printf("dismiss %s: %v", id, err)
		}
	}
	if err := a.publishSessions(ctx); err != nil {
		a.Log.Printf("clear notified %s: %v", id, err)
	}
}

// publishSessions writes the sessions that changed, each marked with whether
// a notification about it is waiting to be seen.
func (a *Agent) publishSessions(ctx context.Context) error {
	if err := a.loadNotified(ctx); err != nil {
		return err
	}
	docs := make(map[string]any, len(a.sessions))
	for id, s := range a.sessions {
		s.Notified = a.notified[id]
		docs[id] = s
	}
	return a.sync(ctx, store.Sessions, docs)
}

// loadNotified recovers, once, the sessions an earlier run notified about,
// whose notifications may still be open on a device. Without it a restart
// would drop their badges and never dismiss their notifications.
func (a *Agent) loadNotified(ctx context.Context) error {
	if a.notifiedLoaded {
		return nil
	}
	if r, ok := a.Store.(store.NotifiedReader); ok {
		ids, err := r.NotifiedSessions(ctx)
		if err != nil {
			return err
		}
		if a.notified == nil {
			a.notified = map[string]bool{}
		}
		for _, id := range ids {
			a.notified[id] = true
		}
	}
	a.notifiedLoaded = true
	return nil
}

// clearedTo is a cleared session's new conversation and the one aoe still
// reports in its place.
type clearedTo struct {
	stale, id string
}

// followClears points cleared sessions at their new conversation while aoe
// still reports the old one, which it keeps doing for up to minutes after a
// /clear. Without this the phone shows the old conversation, as if the clear
// had not happened.
func (a *Agent) followClears(snaps []session.Snapshot) {
	live := map[string]bool{}
	for i := range snaps {
		s := &snaps[i]
		live[s.ID] = true
		c, ok := a.clears[s.ID]
		if !ok {
			continue
		}
		if s.Transcript.ID != c.stale {
			delete(a.clears, s.ID)
			continue
		}
		s.Transcript.ID = c.id
		a.Collect.Transcribe(s)
	}
	for id := range a.clears {
		if !live[id] {
			delete(a.clears, id)
		}
	}
}

// cleared records a session's new conversation and publishes it at once.
func (a *Agent) cleared(ctx context.Context, id, transcript string) error {
	s := a.sessions[id]
	stale := s.Transcript.ID
	if c, ok := a.clears[id]; ok {
		stale = c.stale
	}
	if a.clears == nil {
		a.clears = map[string]clearedTo{}
	}
	a.clears[id] = clearedTo{stale: stale, id: transcript}
	s.Transcript.ID = transcript
	a.Collect.Transcribe(&s)
	a.sessions[id] = s
	if err := a.publishSessions(ctx); err != nil {
		return err
	}
	return a.dropHeld(ctx)
}

// conversationsChanged says whether any session ended, or moved to another
// conversation. On the first refresh it is true, since held replies may have
// outlived their conversation while the agent was stopped.
func conversationsChanged(prev, next map[string]session.Snapshot) bool {
	if prev == nil {
		return true
	}
	for id, p := range prev {
		if n, ok := next[id]; !ok || n.Transcript.ID != p.Transcript.ID {
			return true
		}
	}
	return false
}

// ciRun is what the agent knows about one pushed commit's workflow runs.
type ciRun struct {
	result *session.CI
	pushed time.Time
}

func ciKey(g *session.Git) string {
	if g == nil || g.Repo == "" || g.Pushed == "" {
		return ""
	}
	return g.Repo + "@" + g.Pushed
}

// attachCI gives each snapshot its pushed commit's runs, asking GitHub only
// about commits it has not seen; RefreshCI follows the unfinished ones.
func (a *Agent) attachCI(ctx context.Context, snaps []session.Snapshot) {
	if a.CI == nil {
		return
	}
	live := map[string]bool{}
	for _, s := range snaps {
		key := ciKey(s.Git)
		if key == "" {
			continue
		}
		live[key] = true
		run, ok := a.ciRuns[key]
		if !ok {
			if a.ciRuns == nil {
				a.ciRuns = map[string]*ciRun{}
			}
			run = &ciRun{pushed: a.Now()}
			a.ciRuns[key] = run
			a.lookupCI(ctx, s.Git, run)
		}
		s.Git.CI = run.result
	}
	for key := range a.ciRuns {
		if !live[key] {
			delete(a.ciRuns, key)
		}
	}
}

// lookupCI keeps the last known result when GitHub cannot be reached: the
// board is still right about everything else, and the next poll retries.
func (a *Agent) lookupCI(ctx context.Context, g *session.Git, run *ciRun) {
	res, err := a.CI.Runs(ctx, g.Repo, g.Pushed)
	if err != nil {
		a.Log.Printf("ci: %v", err)
		return
	}
	if res != nil && res.State.Done() && (run.result == nil || !run.result.State.Done()) {
		a.actionsDue = true
	}
	run.result = res
}

func (a *Agent) ciPending() bool {
	for _, run := range a.ciRuns {
		if run.result == nil && a.Now().Sub(run.pushed) < a.CIWait || run.result != nil && !run.result.State.Done() {
			return true
		}
	}
	return false
}

// RefreshCI asks GitHub again about unfinished runs and publishes the
// sessions whose result changed.
func (a *Agent) RefreshCI(ctx context.Context) error {
	if a.sessions == nil {
		return nil
	}
	asked := map[string]bool{}
	prev := a.sessions
	next := make(map[string]session.Snapshot, len(prev))
	for id, s := range prev {
		if key := ciKey(s.Git); key != "" {
			if run := a.ciRuns[key]; run != nil {
				if !asked[key] && (run.result == nil || !run.result.State.Done()) {
					asked[key] = true
					a.lookupCI(ctx, s.Git, run)
				}
				// prev shares this Git, and notify compares the two.
				g := *s.Git
				g.CI = run.result
				s.Git = &g
			}
		}
		next[id] = s
	}
	a.sessions = next
	if err := a.publishSessions(ctx); err != nil {
		return err
	}
	if err := a.notify(ctx, notify.Changes(prev, next)); err != nil {
		return err
	}
	return a.publishSessions(ctx)
}

// RefreshReplies rereads the transcripts of working sessions and publishes
// the ones whose reply or prompt changed. Their state is left to Refresh,
// unless a session has an open question.
func (a *Agent) RefreshReplies(ctx context.Context) error {
	if a.sessions == nil {
		return nil
	}
	for _, s := range a.sessions {
		// The question may have been answered, which changes its state.
		if s.Question != nil {
			return a.Refresh(ctx)
		}
	}
	for id, s := range a.sessions {
		if s.State == session.Working {
			s.SetExchange(a.Collect.Exchange(s.Transcript))
			a.sessions[id] = s
		}
	}
	return a.publishSessions(ctx)
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
		if url := publishedSince(s.Prototypes, t.DispatchedAt); url != "" {
			fields["prototype_url"] = url
		}
		if t.ConversationID == "" && s.Transcript.ID != "" {
			fields["conversation_id"] = s.Transcript.ID
		}
		if t.Ideate != nil {
			n, err := a.fileIdeas(ctx, id, t, s.Ideas)
			if err != nil {
				return err
			}
			fields["ideas"] = n
		}
		if err := a.Store.Update(ctx, store.Tasks, id, fields); err != nil {
			return err
		}
	}
	return nil
}

// ideas returns a project's earlier ideas, or none when the store cannot
// list them.
func (a *Agent) ideas(ctx context.Context, project string) ([]idea.Idea, error) {
	ir, ok := a.Store.(store.IdeaReader)
	if !ok {
		return nil, nil
	}
	return ir.Ideas(ctx, project)
}

// fileIdeas publishes what an ideation task's run reported. Each idea's id
// is the task's and its place, so filing a task's ideas twice rewrites them
// rather than adding copies.
func (a *Agent) fileIdeas(ctx context.Context, taskID string, t store.Task, drafts []idea.Draft) (int, error) {
	known, err := a.ideas(ctx, t.Project)
	if err != nil {
		return 0, err
	}
	at := a.Now()
	ideas := idea.Accept(drafts, *t.Ideate, known)
	for n, i := range ideas {
		i.Project, i.Task, i.CreatedAt = t.Project, taskID, at
		if err := a.Store.Put(ctx, store.Ideas, fmt.Sprintf("%s-%d", taskID, n+1), i); err != nil {
			return n, err
		}
	}
	return len(ideas), nil
}

// serveHistory answers a history request. A failure only logs: the phone may
// have closed the history and deleted the document, and the board is
// unaffected either way.
func (a *Agent) serveHistory(ctx context.Context, r store.HistoryRequest) {
	if a.History == nil {
		return
	}
	fields := map[string]any{"served_at": r.Requested, "entries": []session.Entry{}, "truncated": false, "conversation": ""}
	if s, ok := a.sessions[r.Session]; ok {
		entries, truncated := a.History(s.Transcript)
		if entries == nil {
			entries = []session.Entry{}
		}
		fields["entries"], fields["truncated"], fields["conversation"] = entries, truncated, s.Transcript.ID
	}
	if err := a.Store.Update(ctx, store.History, r.Session, fields); err != nil {
		a.Log.Printf("history for %s: %v", r.Session, err)
	}
}

// PublishUsage writes laptop/usage when the saved usage changed.
func (a *Agent) PublishUsage(ctx context.Context) error {
	if a.Usage == nil {
		return nil
	}
	u, err := a.Usage()
	if err != nil || u == nil {
		return err
	}
	key := contentKey(u)
	if key == a.usageKey {
		return nil
	}
	if a.UsageRead != nil {
		a.UsageRead(u)
	}
	if err := a.Store.Put(ctx, "laptop", "usage", u); err != nil {
		return err
	}
	a.usageKey = key
	return nil
}

// PublishModels writes laptop/models. A failed listing, as when Antigravity
// is signed out, leaves Antigravity's models off the phone rather than
// stopping the agent.
func (a *Agent) PublishModels(ctx context.Context) error {
	if a.Models == nil {
		return nil
	}
	models, err := a.Models(ctx)
	if err != nil {
		a.Log.Printf("antigravity models: %v", err)
		models = nil
	}
	a.models = map[string]bool{}
	for _, m := range models {
		a.models[m.ID] = true
	}
	if models == nil {
		models = []session.Model{}
	}
	return a.Store.Put(ctx, "laptop", "models", session.Models{Antigravity: models})
}

// PublishActions writes laptop/actions when the Actions usage changed. A
// failed lookup leaves the last published usage, which is still the best
// known, and waits for the next finished run.
func (a *Agent) PublishActions(ctx context.Context) error {
	a.actionsDue = false
	if a.Actions == nil {
		return nil
	}
	u, err := a.Actions(ctx)
	if err != nil {
		a.Log.Printf("actions usage: %v", err)
		return nil
	}
	key := contentKey(u)
	if key == a.actionsKey {
		return nil
	}
	if a.ActionsRead != nil {
		a.ActionsRead(u)
	}
	if err := a.Store.Put(ctx, "laptop", "actions", u); err != nil {
		return err
	}
	a.actionsKey = key
	return nil
}

// publishProjects writes the projects of the last refresh, each marked
// hidden or not.
func (a *Agent) publishProjects(ctx context.Context) error {
	if a.hidden == nil && a.Hidden != nil {
		names, err := a.Hidden.Load()
		if err != nil {
			return fmt.Errorf("read hidden projects: %w", err)
		}
		a.hidden = map[string]bool{}
		for _, n := range names {
			a.hidden[n] = true
		}
	}
	docs := make(map[string]any, len(a.projects))
	for name, p := range a.projects {
		p.Hidden = a.hidden[name]
		a.projects[name] = p
		docs[ProjectID(name)] = p
	}
	return a.sync(ctx, store.Projects, docs)
}

// setHidden hides or shows projects and publishes the change at once, so the
// phone need not wait for the next refresh.
func (a *Agent) setHidden(ctx context.Context, names []string, hide bool) error {
	if a.Hidden == nil {
		return errors.New("this agent cannot hide projects")
	}
	if len(names) == 0 {
		return errors.New("name at least one project")
	}
	for _, n := range names {
		if _, ok := a.projects[n]; !ok && hide {
			return fmt.Errorf("no project named %s", n)
		}
	}
	next := map[string]bool{}
	for n := range a.hidden {
		next[n] = true
	}
	for _, n := range names {
		if hide {
			next[n] = true
		} else {
			delete(next, n)
		}
	}
	list := make([]string, 0, len(next))
	for n := range next {
		list = append(list, n)
	}
	sort.Strings(list)
	if err := a.Hidden.Save(list); err != nil {
		return fmt.Errorf("save hidden projects: %w", err)
	}
	a.hidden = next
	return a.publishProjects(ctx)
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

func (a *Agent) setLaptop(ctx context.Context, online bool) error {
	l := store.Laptop{Online: online, SeenAt: a.Now()}
	err := a.Store.SetLaptop(ctx, l)
	if a.LaptopWritten != nil {
		a.LaptopWritten(l, err)
	}
	return err
}

// sleep marks the laptop offline. A failure only logs: the network may already
// be going, and the stale seen_at then tells the phone instead.
func (a *Agent) sleep(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.setLaptop(ctx, false); err != nil {
		a.Log.Printf("mark offline for sleep: %v", err)
	}
}

// wake marks the laptop online and reports whether that was written.
func (a *Agent) wake(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.setLaptop(ctx, true); err != nil {
		a.Log.Printf("mark online after wake: %v", err)
		return false
	}
	return true
}

// Run publishes until ctx ends or something fails. Either way it marks the
// laptop offline before returning, so the phone never shows stale state as live.
func (a *Agent) Run(ctx context.Context, pings <-chan struct{}, cmds <-chan store.Incoming) (err error) {
	defer func() {
		// ctx may be cancelled; the goodbye needs a context of its own.
		bye, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if offErr := a.setLaptop(bye, false); err == nil {
			err = offErr
		}
	}()
	if err := a.Refresh(ctx); err != nil {
		return err
	}
	asleep := a.Asleep != nil && a.Asleep()
	if err := a.setLaptop(ctx, !asleep); err != nil {
		return err
	}
	if err := a.PublishUsage(ctx); err != nil {
		return err
	}
	if err := a.PublishActions(ctx); err != nil {
		return err
	}
	if err := a.PublishModels(ctx); err != nil {
		return err
	}
	presence := time.NewTicker(a.Presence)
	defer presence.Stop()
	var debounce, replies, ci, wake <-chan time.Time
	for {
		if ci == nil && a.ciPending() {
			ci = time.After(a.CIPoll)
		}
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
			if err := a.publishActionsIfDue(ctx); err != nil {
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
		case <-a.UsageChanged:
			if err := a.PublishUsage(ctx); err != nil {
				return err
			}
		case <-ci:
			ci = nil
			if err := a.RefreshCI(ctx); err != nil {
				return err
			}
			if err := a.publishActionsIfDue(ctx); err != nil {
				return err
			}
		case r := <-a.HistoryRequests:
			a.serveHistory(ctx, r)
		case id := <-a.Seen:
			a.seen(ctx, id)
			if err := a.Store.Delete(ctx, store.Seen, id); err != nil {
				a.Log.Printf("seen %s: %v", id, err)
			}
		case in := <-cmds:
			if a.Commands == nil {
				continue
			}
			if err := a.execute(ctx, in); err != nil {
				return err
			}
			if a.restart {
				a.Log.Print("updated; exiting so launchd starts the new build")
				return nil
			}
		case p := <-a.Power:
			asleep, wake = p.Asleep, nil
			if p.Asleep {
				a.sleep(ctx)
				p.Done()
			} else if !a.wake(ctx) {
				wake = time.After(powerRetry)
			}
		case <-wake:
			wake = nil
			if !a.wake(ctx) {
				wake = time.After(powerRetry)
			}
		case <-presence.C:
			// A dark wake runs this loop, but the laptop is still asleep.
			if asleep {
				continue
			}
			if err := a.setLaptop(ctx, true); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) publishActionsIfDue(ctx context.Context) error {
	if !a.actionsDue {
		return nil
	}
	return a.PublishActions(ctx)
}
