package agent

import (
	"context"
	"errors"
	"fmt"
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

type unreachableLaptop struct {
	*store.Memory
	err error
}

func (u *unreachableLaptop) SetLaptop(ctx context.Context, l store.Laptop) error {
	if u.err != nil {
		return u.err
	}
	return u.Memory.SetLaptop(ctx, l)
}

func TestEveryLaptopStatusWriteIsReportedWithItsError(t *testing.T) {
	s := &unreachableLaptop{Memory: store.NewMemory(), err: errors.New("deadline exceeded")}
	a := newAgent(&fakeCollector{}, s)
	var got []string
	a.LaptopWritten = func(l store.Laptop, err error) {
		got = append(got, fmt.Sprintf("%v %v", l.Online, err))
	}
	if a.wake(context.Background()) {
		t.Fatal("wake reported a failed write as written")
	}
	s.err = nil
	if !a.wake(context.Background()) {
		t.Fatal("wake failed")
	}
	a.sleep(context.Background())
	if want := "true deadline exceeded,true <nil>,false <nil>"; strings.Join(got, ",") != want {
		t.Fatalf("reported %q, want %q", strings.Join(got, ","), want)
	}
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
		if n.Dismiss {
			r.notes = append(r.notes, "dismiss "+n.Session)
			continue
		}
		r.notes = append(r.notes, n.Title+": "+n.Body)
	}
	return nil
}

func TestSeenDismissesANotifiedSessionEverywhereOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mem := store.NewMemory()
	c := &fakeCollector{}
	n := &recordingNotifier{}
	a := newAgent(c, mem)
	a.Notify = n
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Working}, session.Snapshot{ID: "h", Title: "hamta", State: session.Idle})
	_ = a.Refresh(ctx)
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Idle}, session.Snapshot{ID: "h", Title: "hamta", State: session.Idle})
	_ = a.Refresh(ctx)
	seen := make(chan string)
	a.Seen = seen
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, nil, nil) }()

	for _, id := range []string{"k", "k", "h"} {
		_ = mem.Put(ctx, store.Seen, id, map[string]any{})
		seen <- id
	}
	seen <- "sync"
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(n.notes, "|"); got != "knip: Finished|dismiss k" {
		t.Fatalf("notes = %v", got)
	}
	if ids, _ := mem.IDs(ctx, store.Seen); len(ids) != 0 {
		t.Fatalf("seen left: %v", ids)
	}
}

func notified(t *testing.T, mem *store.Memory, id string) bool {
	t.Helper()
	v, ok := mem.Get(store.Sessions, id)
	if !ok {
		t.Fatalf("session %s not published", id)
	}
	return v.(session.Snapshot).Notified
}

func TestASessionIsPublishedNotifiedUntilADeviceShowsIt(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	c := &fakeCollector{}
	a := newAgent(c, mem)
	a.Notify = &recordingNotifier{}
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Working}, session.Snapshot{ID: "h", Title: "hamta", State: session.Idle})
	_ = a.Refresh(ctx)
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Idle}, session.Snapshot{ID: "h", Title: "hamta", State: session.Idle})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !notified(t, mem, "k") || notified(t, mem, "h") {
		t.Fatalf("notified: k=%v h=%v", notified(t, mem, "k"), notified(t, mem, "h"))
	}
	_ = a.Refresh(ctx)
	if !notified(t, mem, "k") {
		t.Fatal("a later refresh cleared the badge")
	}
	a.seen(ctx, "k")
	if notified(t, mem, "k") {
		t.Fatal("still notified after being seen")
	}
}

func TestARestartKeepsBadgesAndStillDismisses(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	c := &fakeCollector{}
	first := newAgent(c, mem)
	first.Notify = &recordingNotifier{}
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Working})
	_ = first.Refresh(ctx)
	c.set(session.Snapshot{ID: "k", Title: "knip", State: session.Idle})
	_ = first.Refresh(ctx)

	n := &recordingNotifier{}
	second := newAgent(c, mem)
	second.Notify = n
	if err := second.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !notified(t, mem, "k") {
		t.Fatal("restart dropped the badge")
	}
	second.seen(ctx, "k")
	if notified(t, mem, "k") || strings.Join(n.notes, "|") != "dismiss k" {
		t.Fatalf("notified=%v notes=%v", notified(t, mem, "k"), n.notes)
	}
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

func TestRunRereadsActionsUsageWhenAFollowedRunFinishes(t *testing.T) {
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "k", Title: "knip", Git: &session.Git{Pushed: "abc", Repo: "maclir/knip"}})
	ci := &fakeCI{runs: map[string]*session.CI{"maclir/knip@abc": {SHA: "abc", State: session.CIRunning}}}
	a := newAgent(c, mem)
	a.CI, a.CIPoll, a.CIWait = ci, 10*time.Millisecond, time.Minute
	var mu sync.Mutex
	used, asked := 100, 0
	a.Actions = func(context.Context) (*session.Actions, error) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		return &session.Actions{Included: 2000, Used: used}, nil
	}
	published := func() int {
		v, ok := mem.Get("laptop", "actions")
		if !ok {
			return -1
		}
		return v.(*session.Actions).Used
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, nil, nil) }()

	waitFor(t, func() bool { return published() == 100 })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if asked != 1 {
		t.Errorf("asked for Actions usage %d times while the run was unfinished, want 1", asked)
	}
	used = 130
	mu.Unlock()
	ci.set("maclir/knip@abc", &session.CI{SHA: "abc", State: session.CISuccess})
	waitFor(t, func() bool { return published() == 130 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublishActionsKeepsTheLastUsageWhenGitHubFails(t *testing.T) {
	mem := store.NewMemory()
	a := newAgent(&fakeCollector{}, mem)
	a.Actions = func(context.Context) (*session.Actions, error) { return &session.Actions{Used: 5}, nil }
	ctx := context.Background()
	if err := a.PublishActions(ctx); err != nil {
		t.Fatal(err)
	}
	a.Actions = func(context.Context) (*session.Actions, error) { return nil, errors.New("404 Not Found") }
	if err := a.PublishActions(ctx); err != nil {
		t.Fatalf("a GitHub failure stopped the agent: %v", err)
	}
	if v, _ := mem.Get("laptop", "actions"); v.(*session.Actions).Used != 5 {
		t.Fatalf("published %+v, want the last known usage", v)
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

func TestRefreshReportsThePublishedSessions(t *testing.T) {
	c := &fakeCollector{}
	a := newAgent(c, store.NewMemory())
	var got []string
	var at time.Time
	a.Refreshed = func(t time.Time, snaps []session.Snapshot) {
		at = t
		for _, s := range snaps {
			got = append(got, s.ID)
		}
	}
	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle})
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "k" || at.IsZero() {
		t.Fatalf("reported %v at %v", got, at)
	}
}

func TestRunPublishesAHistoryOnlyWhenAsked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mem := store.NewMemory()
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", Transcript: session.Transcript{Dir: "/t", ID: "conv"}})
	a := newAgent(c, mem)
	read := make(chan session.Transcript, 1)
	a.History = func(t session.Transcript) ([]session.Entry, bool) {
		read <- t
		return []session.Entry{{Kind: session.EntryPrompt, Text: "hi"}}, true
	}
	requests := make(chan store.HistoryRequest)
	a.HistoryRequests = requests
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, nil, nil) }()

	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	requests <- store.HistoryRequest{Session: "k", Requested: at}
	if t2 := <-read; t2.ID != "conv" {
		t.Fatalf("read %+v", t2)
	}
	requests <- store.HistoryRequest{Session: "gone", Requested: at}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := mem.Updated(store.History, "k")
	entries, _ := got["entries"].([]session.Entry)
	if len(entries) != 1 || got["truncated"] != true || got["conversation"] != "conv" || got["served_at"] != at {
		t.Fatalf("history = %+v", got)
	}
	if gone := mem.Updated(store.History, "gone"); gone["served_at"] != at || len(gone["entries"].([]session.Entry)) != 0 {
		t.Fatalf("an ended session's request is answered empty: %+v", gone)
	}
}

func TestHistoryRequestIsPendingUntilServed(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		r    store.HistoryRequest
		want bool
	}{
		{store.HistoryRequest{Requested: at}, true},
		{store.HistoryRequest{Requested: at, Served: at}, false},
		{store.HistoryRequest{Requested: at.Add(time.Second), Served: at}, true},
		{store.HistoryRequest{}, false},
	} {
		if got := tc.r.Pending(); got != tc.want {
			t.Errorf("%+v pending = %v", tc.r, got)
		}
	}
}

type unreachablePut struct{ *store.Memory }

func (unreachablePut) Put(context.Context, string, string, any) error {
	return errors.New("deadline exceeded")
}

func TestQuotasReachTheMenuBarOncePerChange(t *testing.T) {
	a := newAgent(&fakeCollector{}, store.NewMemory())
	used := 10.0
	a.Usage = func() (*session.Usage, error) { return &session.Usage{FiveHour: &session.Window{Used: used}}, nil }
	a.Actions = func(context.Context) (*session.Actions, error) { return &session.Actions{Used: int(used)}, nil }
	var got []string
	a.UsageRead = func(u *session.Usage) { got = append(got, fmt.Sprintf("claude %v", u.FiveHour.Used)) }
	a.ActionsRead = func(u *session.Actions) { got = append(got, fmt.Sprintf("actions %v", u.Used)) }
	ctx := context.Background()
	for _, u := range []float64{10, 10, 12} {
		used = u
		if err := a.PublishUsage(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.PublishActions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if want := "claude 10,actions 10,claude 12,actions 12"; strings.Join(got, ",") != want {
		t.Fatalf("told %q, want %q", strings.Join(got, ","), want)
	}
}

func TestQuotasReachTheMenuBarWhenFirestoreFails(t *testing.T) {
	a := newAgent(&fakeCollector{}, unreachablePut{store.NewMemory()})
	a.Usage = func() (*session.Usage, error) { return &session.Usage{}, nil }
	a.Actions = func(context.Context) (*session.Actions, error) { return &session.Actions{}, nil }
	told := 0
	a.UsageRead = func(*session.Usage) { told++ }
	a.ActionsRead = func(*session.Actions) { told++ }
	a.PublishUsage(context.Background())
	a.PublishActions(context.Background())
	if told != 2 {
		t.Fatalf("told the menu bar %d quotas, want 2", told)
	}
}

func TestRefreshFollowsAoeHooksOnlyForSessionsThatHaveThem(t *testing.T) {
	c := &fakeCollector{}
	c.set(session.Snapshot{ID: "a", Path: "/a"}, session.Snapshot{ID: "b", Path: "/b", AoeHooks: "/hooks/b"})
	w := &recordingWatcher{}
	a := newAgent(c, store.NewMemory())
	a.Statuses = w
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.dirs, ",") != "/hooks/b" {
		t.Fatalf("watched %v", w.dirs)
	}
}
