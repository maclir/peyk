package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

type fakeCollector struct {
	mu    sync.Mutex
	snaps []session.Snapshot
	err   error
	calls int
	// replies holds what each transcript, by id, now says.
	replies map[string]*session.Message
}

func (f *fakeCollector) Exchange(t session.Transcript) (*session.Message, *session.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replies[t.ID], nil
}

func (f *fakeCollector) Transcribe(s *session.Snapshot) {
	s.SetExchange(f.Exchange(s.Transcript))
}

func (f *fakeCollector) All(context.Context) ([]session.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return append([]session.Snapshot(nil), f.snaps...), f.err
}

func (f *fakeCollector) Repos() ([]string, error) { return []string{"/c/sakku"}, nil }

func (f *fakeCollector) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeCollector) set(s ...session.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps = s
}

func (f *fakeCollector) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newAgent(c Collector, s store.Store) *Agent {
	clock := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	return &Agent{
		Collect: c, Store: s, Debounce: 20 * time.Millisecond, Presence: time.Hour,
		Now: func() time.Time { clock = clock.Add(time.Second); return clock },
		Log: log.New(io.Discard, "", 0),
	}
}

func TestRefreshWritesOnlyChangesAndRemovesGoneDocuments(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	_ = mem.Put(ctx, store.Sessions, "stale", session.Snapshot{ID: "stale"})
	_ = mem.Put(ctx, store.Projects, "gone", session.Project{Name: "gone"})
	c := &fakeCollector{}
	a := newAgent(c, mem)

	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, UpdatedAt: time.Unix(1, 0)})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, _ := mem.IDs(ctx, store.Sessions); strings.Join(ids, ",") != "k" {
		t.Fatalf("sessions = %v", ids)
	}
	if ids, _ := mem.IDs(ctx, store.Projects); strings.Join(ids, ",") != "knip,sakku" {
		t.Fatalf("projects = %v", ids)
	}
	if v, _ := mem.Get(store.Projects, "knip"); !v.(session.Project).Free || v.(session.Project).SessionID != "k" {
		t.Fatalf("project knip = %+v", v)
	}
	writes := mem.Writes()

	// Same content, newer timestamp: nothing to write.
	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, UpdatedAt: time.Unix(2, 0)})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if mem.Writes() != writes {
		t.Fatalf("unchanged documents were rewritten: %d writes, want %d", mem.Writes(), writes)
	}

	// A busy session changes both its session and its project.
	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Working})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if mem.Writes() != writes+2 {
		t.Fatalf("writes = %d, want %d", mem.Writes(), writes+2)
	}
	if v, _ := mem.Get(store.Projects, "knip"); v.(session.Project).Free || v.(session.Project).Busy != "working" {
		t.Fatalf("project knip = %+v", v)
	}
}

type logoCollector struct{ fakeCollector }

func (*logoCollector) Logo(dir string) string {
	if dir == "/c/sakku" {
		return "data:image/png;base64,AA=="
	}
	return ""
}

func TestRefreshPublishesEachProjectsLogo(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	c := &logoCollector{}
	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle})
	if err := newAgent(c, mem).Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := mem.Get(store.Projects, "sakku"); v.(session.Project).Logo != "data:image/png;base64,AA==" {
		t.Fatalf("project sakku = %+v", v)
	}
	if v, _ := mem.Get(store.Projects, "knip"); v.(session.Project).Logo != "" {
		t.Fatalf("project knip = %+v", v)
	}
}

func TestProjectIDDropsSlashes(t *testing.T) {
	if got := ProjectID("a/b"); got != "a_b" {
		t.Fatal(got)
	}
}

func TestRunCoalescesPingsAndSaysGoodbye(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "k", Title: "knip"})
	a := newAgent(c, mem)
	ctx, cancel := context.WithCancel(context.Background())
	pings := make(chan struct{}, 1)
	done := make(chan error)
	go func() { done <- a.Run(ctx, pings, nil) }()

	waitFor(t, func() bool { return mem.Laptop().Online })
	if c.count() != 1 {
		t.Fatalf("start should refresh once, got %d", c.count())
	}
	for i := 0; i < 5; i++ {
		pings <- struct{}{}
	}
	waitFor(t, func() bool { return c.count() >= 2 })
	time.Sleep(60 * time.Millisecond)
	if n := c.count(); n != 2 {
		t.Fatalf("five quick pings should refresh once, got %d refreshes after start", n-1)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mem.Laptop().Online {
		t.Fatal("laptop still online after stop")
	}
}

func TestRunStopsOnAFailedRefreshAndGoesOffline(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	a := newAgent(c, mem)
	pings := make(chan struct{}, 1)
	done := make(chan error)
	go func() { done <- a.Run(context.Background(), pings, nil) }()
	waitFor(t, func() bool { return mem.Laptop().Online })

	c.fail(errors.New("aoe list: exit status 1"))
	pings <- struct{}{}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "aoe list") {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after a failed refresh")
	}
	if mem.Laptop().Online {
		t.Fatal("laptop left online after a failure")
	}
}

func TestRunFailsAtStartWithoutGoingOnline(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{err: errors.New("broken")}
	if err := newAgent(c, mem).Run(context.Background(), nil, nil); err == nil {
		t.Fatal("want error")
	}
	if mem.Laptop().Online {
		t.Fatal("online after a failed start")
	}
}

func TestRunGoesOfflineForSleepAndStaysOfflineUntilAFullWake(t *testing.T) {
	mem := store.NewMemory()
	a := newAgent(&fakeCollector{}, mem)
	a.Presence = 5 * time.Millisecond
	power := make(chan PowerEvent)
	a.Power = power
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx, nil, nil)
	waitFor(t, func() bool { return mem.Laptop().Online })

	acked := make(chan struct{})
	power <- PowerEvent{Asleep: true, Done: func() {
		if mem.Laptop().Online {
			t.Error("sleep allowed before the laptop was marked offline")
		}
		close(acked)
	}}
	<-acked
	time.Sleep(30 * time.Millisecond)
	if mem.Laptop().Online {
		t.Fatal("presence marked a sleeping laptop online")
	}

	power <- PowerEvent{}
	waitFor(t, func() bool { return mem.Laptop().Online })
}

type recordingWatcher struct{ dirs []string }

func (r *recordingWatcher) Set(_ context.Context, dirs []string) error {
	r.dirs = dirs
	return nil
}

func TestRefreshPointsTheWatcherAtSessionDirectories(t *testing.T) {
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "a", Path: "/a"}, session.Snapshot{ID: "b", Path: "/b"})
	w := &recordingWatcher{}
	a := newAgent(c, store.NewMemory())
	a.Watch = w
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.dirs, ",") != "/a,/b" {
		t.Fatalf("watched %v", w.dirs)
	}
}

func TestSocketPingReachesAgentAndPingWithoutAgentIsSilent(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "a.sock")
	Ping(path) // nothing listening: must not block or fail

	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("second agent should be refused")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pings := make(chan struct{}, 1)
	go Serve(ctx, ln, pings)
	Ping(path)
	select {
	case <-pings:
	case <-time.After(2 * time.Second):
		t.Fatal("ping not delivered")
	}
}

func TestListenReplacesSocketOfDeadAgent(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "a.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: the file stays, nobody accepts.
	if ul, ok := ln.(interface{ SetUnlinkOnClose(bool) }); ok {
		ul.SetUnlinkOnClose(false)
	}
	ln.Close()
	ln, err = Listen(path)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	ln.Close()
}

// Unix socket paths are limited to about 100 bytes, and t.TempDir on macOS
// is longer than that once the test name is in it.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "peyk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type recordingNotifier struct{ notes []string }

func (r *recordingNotifier) Notify(_ context.Context, notes []notify.Note) error {
	for _, n := range notes {
		r.notes = append(r.notes, n.Title+": "+n.Body)
	}
	return nil
}

func TestRefreshNotifiesOnTransitionsButNotAtStart(t *testing.T) {
	ctx := context.Background()
	c := &fakeCollector{}
	n := &recordingNotifier{}
	a := newAgent(c, store.NewMemory())
	a.Notify = n
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.NeedsInput})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(n.notes) != 0 {
		t.Fatalf("notified at start: %v", n.notes)
	}
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Working})
	_ = a.Refresh(ctx)
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Idle})
	_ = a.Refresh(ctx)
	if strings.Join(n.notes, "|") != "knip: Finished" {
		t.Fatalf("notes = %v", n.notes)
	}
}

func TestRefreshWatchesEditsOnlyInSessionsNotWorking(t *testing.T) {
	c := &fakeCollector{}
	c.set(
		session.Snapshot{ID: "a", Path: "/a", State: session.Working, Git: &session.Git{}},
		session.Snapshot{ID: "b", Path: "/b", State: session.Idle, Git: &session.Git{}},
		session.Snapshot{ID: "c", Path: "/c", State: session.NeedsInput, Git: &session.Git{}},
		session.Snapshot{ID: "d", Path: "/d", State: session.Idle},
	)
	w := &recordingWatcher{}
	a := newAgent(c, store.NewMemory())
	a.Edits = w
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.dirs, ",") != "/b,/c" {
		t.Fatalf("watched %v", w.dirs)
	}
}

func TestRepliesRepublishOnlyWorkingSessionsWithoutAFullRefresh(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(
		session.Snapshot{ID: "w", Title: "working", State: session.Working, Transcript: session.Transcript{Dir: "/t/w", ID: "tw"}},
		session.Snapshot{ID: "i", Title: "idle", State: session.Idle, Transcript: session.Transcript{Dir: "/t/i", ID: "ti"}},
	)
	w := &recordingWatcher{}
	a := newAgent(c, mem)
	a.Transcripts = w
	replies := make(chan struct{}, 1)
	a.Replies = replies
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, nil, nil) }()
	waitFor(t, func() bool { return mem.Laptop().Online })
	if strings.Join(w.dirs, ",") != "/t/w" {
		t.Fatalf("watched %v, want only the working session's transcripts", w.dirs)
	}

	c.mu.Lock()
	c.replies = map[string]*session.Message{"tw": {Text: "halfway"}, "ti": {Text: "stale"}}
	c.mu.Unlock()
	replies <- struct{}{}
	waitFor(t, func() bool {
		v, _ := mem.Get(store.Sessions, "w")
		s, ok := v.(session.Snapshot)
		return ok && s.Message != nil && s.Message.Text == "halfway"
	})
	if v, _ := mem.Get(store.Sessions, "i"); v.(session.Snapshot).Message != nil {
		t.Fatalf("an idle session was reread: %+v", v.(session.Snapshot).Message)
	}
	if c.count() != 1 {
		t.Fatalf("a reply caused %d full refreshes", c.count()-1)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestATranscriptWriteRefreshesASessionWithAnOpenQuestion(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(
		session.Snapshot{ID: "q", Title: "quiz", State: session.NeedsInput, Transcript: session.Transcript{Dir: "/t/q", ID: "tq"},
			Question: &session.Question{ID: "toolu_1"}},
		session.Snapshot{ID: "i", Title: "idle", State: session.Idle, Transcript: session.Transcript{Dir: "/t/i", ID: "ti"}},
	)
	w := &recordingWatcher{}
	a := newAgent(c, mem)
	a.Transcripts = w
	replies := make(chan struct{}, 1)
	a.Replies = replies
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, nil, nil) }()
	waitFor(t, func() bool { return mem.Laptop().Online })
	if strings.Join(w.dirs, ",") != "/t/q" {
		t.Fatalf("watched %v, want the asking session's transcripts", w.dirs)
	}

	c.set(session.Snapshot{ID: "q", Title: "quiz", State: session.Working, Transcript: session.Transcript{Dir: "/t/q", ID: "tq"}})
	replies <- struct{}{}
	waitFor(t, func() bool {
		v, _ := mem.Get(store.Sessions, "q")
		s, ok := v.(session.Snapshot)
		return ok && s.Question == nil && s.State == session.Working
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type fakeCI struct {
	mu    sync.Mutex
	runs  map[string]*session.CI
	calls int
}

func (f *fakeCI) Runs(_ context.Context, repo, sha string) (*session.CI, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r := f.runs[repo+"@"+sha]; r != nil {
		c := *r
		return &c, nil
	}
	return nil, nil
}

func (f *fakeCI) set(key string, ci *session.CI) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[key] = ci
}

func TestCIIsFollowedUntilTheRunFinishes(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	c := &fakeCollector{}
	n := &recordingNotifier{}
	ci := &fakeCI{runs: map[string]*session.CI{}}
	a := newAgent(c, mem)
	a.Notify, a.CI, a.CIWait = n, ci, time.Minute
	pushed := func() session.Snapshot {
		return session.Snapshot{ID: "k", Title: "knip", State: session.Idle,
			Git: &session.Git{Upstream: "origin/main", Pushed: "abc", Repo: "maclir/knip"}}
	}
	other := session.Snapshot{ID: "o", Title: "other", State: session.Idle, Git: &session.Git{Upstream: "origin/main"}}

	c.set(pushed(), other)
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ci.calls != 1 || !a.ciPending() {
		t.Fatalf("a fresh push: %d lookups, pending %v", ci.calls, a.ciPending())
	}

	ci.set("maclir/knip@abc", &session.CI{SHA: "abc", Workflow: "deploy", State: session.CIRunning, URL: "https://github.com/run/1"})
	if err := a.RefreshCI(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := mem.Get(store.Sessions, "k"); v.(session.Snapshot).Git.CI.State != session.CIRunning {
		t.Fatalf("published %+v", v.(session.Snapshot).Git)
	}

	// A refresh for something else does not ask GitHub again.
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ci.calls != 2 {
		t.Fatalf("lookups = %d, want 2", ci.calls)
	}
	if v, _ := mem.Get(store.Sessions, "k"); v.(session.Snapshot).Git.CI == nil {
		t.Fatal("a refresh dropped the known run")
	}

	ci.set("maclir/knip@abc", &session.CI{SHA: "abc", Workflow: "deploy", State: session.CIFailure, URL: "https://github.com/run/1"})
	if err := a.RefreshCI(ctx); err != nil {
		t.Fatal(err)
	}
	if a.ciPending() {
		t.Fatal("still following a finished run")
	}
	if strings.Join(n.notes, "|") != "knip: deploy failed · abc" {
		t.Fatalf("notes = %v", n.notes)
	}
	if v, _ := mem.Get(store.Sessions, "k"); v.(session.Snapshot).Git.CI.URL != "https://github.com/run/1" {
		t.Fatalf("published %+v", v.(session.Snapshot).Git.CI)
	}
}

func TestCIStopsWaitingForARunThatNeverComes(t *testing.T) {
	ctx := context.Background()
	c := &fakeCollector{}
	a := newAgent(c, store.NewMemory())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	a.CI, a.CIWait = &fakeCI{runs: map[string]*session.CI{}}, 90*time.Second
	c.set(session.Snapshot{ID: "k", Title: "knip", Git: &session.Git{Pushed: "abc", Repo: "maclir/knip"}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.ciPending() {
		t.Fatal("not waiting for a run right after a push")
	}
	now = now.Add(90 * time.Second)
	if a.ciPending() {
		t.Fatal("still waiting after CIWait")
	}
}

func TestRunAsksGitHubAgainWhileARunIsUnfinished(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "k", Title: "knip", Git: &session.Git{Pushed: "abc", Repo: "maclir/knip"}})
	ci := &fakeCI{runs: map[string]*session.CI{"maclir/knip@abc": {SHA: "abc", State: session.CIQueued}}}
	a := newAgent(c, mem)
	a.CI, a.CIPoll, a.CIWait = ci, 10*time.Millisecond, time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, nil, nil) }()

	waitFor(t, func() bool { return mem.Laptop().Online })
	ci.set("maclir/knip@abc", &session.CI{SHA: "abc", State: session.CISuccess})
	waitFor(t, func() bool {
		v, _ := mem.Get(store.Sessions, "k")
		return v.(session.Snapshot).Git.CI.State == session.CISuccess
	})
	ci.mu.Lock()
	calls := ci.calls
	ci.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.calls != calls {
		t.Fatalf("asked GitHub %d more times after the run finished", ci.calls-calls)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublishUsageWritesOnlyWhenItChanged(t *testing.T) {
	mem := store.NewMemory()
	a := newAgent(&fakeCollector{}, mem)
	var saved *session.Usage
	a.Usage = func() (*session.Usage, error) { return saved, nil }
	ctx := context.Background()

	if err := a.PublishUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := mem.Get("laptop", "usage"); ok {
		t.Fatal("published usage before any was saved")
	}
	saved = &session.Usage{FiveHour: &session.Window{Used: 10, ResetsAt: time.Unix(100, 0)}}
	if err := a.PublishUsage(ctx); err != nil {
		t.Fatal(err)
	}
	writes := mem.Writes()
	if err := a.PublishUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if mem.Writes() != writes {
		t.Fatal("republished unchanged usage")
	}
	saved = &session.Usage{FiveHour: &session.Window{Used: 12, ResetsAt: time.Unix(100, 0)}}
	if err := a.PublishUsage(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := mem.Get("laptop", "usage")
	if got.(*session.Usage).FiveHour.Used != 12 {
		t.Fatalf("published %+v, want the latest usage", got)
	}
}
