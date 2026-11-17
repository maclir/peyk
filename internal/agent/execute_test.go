package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

const (
	testOrigin = "https://peyk.test"
	testRP     = "peyk.test"
)

type fakeActions struct{ calls []string }

func (f *fakeActions) Send(_ context.Context, s, text string) error {
	f.calls = append(f.calls, "send "+s+": "+text)
	return nil
}
func (f *fakeActions) Create(_ context.Context, dir, title string) (string, error) {
	f.calls = append(f.calls, "create "+title+" in "+dir)
	return "new-" + title, nil
}
func (f *fakeActions) Clear(_ context.Context, s string, _ session.Transcript) (string, error) {
	f.calls = append(f.calls, "clear "+s)
	return "cleared-" + s, nil
}
func (f *fakeActions) Stop(_ context.Context, s string) error {
	f.calls = append(f.calls, "stop "+s)
	return nil
}
func (f *fakeActions) Start(_ context.Context, s string) error {
	f.calls = append(f.calls, "start "+s)
	return nil
}
func (f *fakeActions) Restart(_ context.Context, s string) error {
	f.calls = append(f.calls, "restart "+s)
	return nil
}
func (f *fakeActions) Resume(_ context.Context, s string, _ session.Transcript, conversation string, stopped bool) error {
	how := "restart"
	if stopped {
		how = "start"
	}
	f.calls = append(f.calls, "resume "+s+" in "+conversation+" by "+how)
	return nil
}
func (f *fakeActions) Push(_ context.Context, dir string) error {
	f.calls = append(f.calls, "push "+dir)
	return nil
}
func (f *fakeActions) Answer(_ context.Context, s string, tr session.Transcript, question, text string) error {
	f.calls = append(f.calls, "answer "+s+" in "+tr.ID+" to "+question+": "+text)
	return nil
}

type phone struct {
	priv *ecdsa.PrivateKey
	key  command.Key
}

func newPhone(t *testing.T) phone {
	t.Helper()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	k, err := command.NewKey("pixel", "cred", base64.StdEncoding.EncodeToString(der), command.ES256, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return phone{priv: priv, key: k}
}

// command builds a command document the way the phone does: payload, then a
// WebAuthn assertion over it unless the type needs none.
func (ph phone) command(id string, p command.Payload, now time.Time) store.Incoming {
	p.ID = id
	if p.Expires == 0 {
		p.Expires = now.Add(time.Minute).UnixMilli()
	}
	payload, _ := json.Marshal(p)
	c := command.Command{Type: p.Type, Payload: string(payload), Status: command.Pending}
	if command.Signed(p.Type) {
		enc := base64.RawURLEncoding
		ch := sha256.Sum256(payload)
		client, _ := json.Marshal(map[string]string{"type": "webauthn.get", "challenge": enc.EncodeToString(ch[:]), "origin": testOrigin})
		rp := sha256.Sum256([]byte(testRP))
		auth := append(append([]byte{}, rp[:]...), 0x05, 0, 0, 0, 1)
		ch2 := sha256.Sum256(client)
		digest := sha256.Sum256(append(append([]byte{}, auth...), ch2[:]...))
		sig, _ := ecdsa.SignASN1(rand.Reader, ph.priv, digest[:])
		c.Assertion = &command.Assertion{
			CredentialID: ph.key.CredentialID, AuthenticatorData: enc.EncodeToString(auth),
			ClientDataJSON: enc.EncodeToString(client), Signature: enc.EncodeToString(sig),
		}
	}
	return store.Incoming{ID: id, Command: c}
}

func commandAgent(t *testing.T, ph phone) (*Agent, *store.Memory, *fakeActions) {
	t.Helper()
	c := &fakeCollector{}
	c.set(
		session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, Transcript: session.Transcript{ID: "now"}},
		session.Snapshot{ID: "h", Title: "hamta", Path: "/c/hamta", State: session.Working},
		session.Snapshot{ID: "z", Title: "zynk", Path: "/c/zynk", State: session.Unpushed, Git: &session.Git{Unpushed: 1}},
		session.Snapshot{ID: "d", Title: "dorna", Path: "/c/dorna", State: session.Dirty, Git: &session.Git{Dirty: 2}},
		session.Snapshot{ID: "f", Title: "fresh", Path: "/c/fresh", State: session.Idle, Cleared: true},
		session.Snapshot{ID: "s", Title: "sleepy", Path: "/c/sleepy", State: session.Stopped, AoeStatus: "stopped"},
		session.Snapshot{ID: "q", Title: "quiz", Path: "/c/quiz", State: session.NeedsInput,
			Transcript: session.Transcript{ID: "conv"}, Question: &session.Question{ID: "toolu_1"}},
	)
	mem := store.NewMemory()
	acts := &fakeActions{}
	a := newAgent(c, mem)
	a.Commands = &Commands{Act: acts, Keys: func() ([]command.Key, error) { return []command.Key{ph.key}, nil }, Origin: testOrigin, RPID: testRP}
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, mem, acts
}

func TestExecuteCommands(t *testing.T) {
	ph := newPhone(t)
	tests := []struct {
		name    string
		payload command.Payload
		calls   string
		failure string
	}{
		{"dispatch to a free session", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "Add --json"}, "send k: Add --json", ""},
		{"clear, then dispatch", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "Add --json", Clear: true}, "clear k|send k: Add --json", ""},
		{"clear an unpushed session, then dispatch", command.Payload{Type: command.Dispatch, Project: "zynk", Task: "t1", Text: "x", Clear: true}, "clear z|send z: x", ""},
		{"dispatch to a session already cleared", command.Payload{Type: command.Dispatch, Project: "fresh", Task: "t1", Text: "x", Clear: true}, "send f: x", ""},
		{"a new session needs no clearing", command.Payload{Type: command.Dispatch, Project: "sakku", Task: "t1", Text: "Fix login", Clear: true}, "create sakku in /c/sakku|send new-sakku: Fix login", ""},
		{"dispatch to a repository without a session", command.Payload{Type: command.Dispatch, Project: "sakku", Task: "t1", Text: "Fix login"}, "create sakku in /c/sakku|send new-sakku: Fix login", ""},
		{"dispatch to a busy session", command.Payload{Type: command.Dispatch, Project: "hamta", Task: "t1", Text: "x"}, "", "hamta is not free: working"},
		{"dispatch to a session with only unpushed commits", command.Payload{Type: command.Dispatch, Project: "zynk", Task: "t1", Text: "x"}, "send z: x", ""},
		{"dispatch to a session with uncommitted changes", command.Payload{Type: command.Dispatch, Project: "dorna", Task: "t1", Text: "x"}, "", "dorna is not free: uncommitted changes"},
		{"dispatch to an unknown project", command.Payload{Type: command.Dispatch, Project: "nope", Task: "t1", Text: "x"}, "", "no project named nope"},
		{"reply to a working session", command.Payload{Type: command.Reply, Session: "h", Text: "Only the current filter"}, "send h: Only the current filter", ""},
		{"reply without text or images", command.Payload{Type: command.Reply, Session: "h"}, "", "reply needs text or an image"},
		{"clear an idle session", command.Payload{Type: command.Clear, Session: "k"}, "clear k", ""},
		{"clearing a cleared session does nothing", command.Payload{Type: command.Clear, Session: "f"}, "", ""},
		{"clear a working session", command.Payload{Type: command.Clear, Session: "h"}, "", "hamta is working"},
		{"stop", command.Payload{Type: command.Stop, Session: "h"}, "stop h", ""},
		{"clear, then stop", command.Payload{Type: command.Stop, Session: "k", Clear: true}, "clear k|stop k", ""},
		{"stop a session already cleared", command.Payload{Type: command.Stop, Session: "f", Clear: true}, "stop f", ""},
		{"clear and stop a working session", command.Payload{Type: command.Stop, Session: "h", Clear: true}, "", "hamta is working"},
		{"start a stopped session", command.Payload{Type: command.Start, Session: "s"}, "start s", ""},
		{"start a running session", command.Payload{Type: command.Start, Session: "h"}, "", "hamta is already running"},
		{"restart a running session", command.Payload{Type: command.Restart, Session: "h"}, "restart h", ""},
		{"restart a stopped session", command.Payload{Type: command.Restart, Session: "s"}, "", "sleepy is stopped; start it instead"},
		{"resume a free session", command.Payload{Type: command.Resume, Session: "k", Conversation: "older"}, "resume k in older by restart", ""},
		{"resume a stopped session", command.Payload{Type: command.Resume, Session: "s", Conversation: "older"}, "resume s in older by start", ""},
		{"resume the conversation already open", command.Payload{Type: command.Resume, Session: "k", Conversation: "now"}, "", ""},
		{"resume a working session", command.Payload{Type: command.Resume, Session: "h", Conversation: "older"}, "", "hamta is not free: working"},
		{"resume a session asking a question", command.Payload{Type: command.Resume, Session: "q", Conversation: "older"}, "", "quiz is not free: needs input"},
		{"resume a session with uncommitted changes", command.Payload{Type: command.Resume, Session: "d", Conversation: "older"}, "", "dorna is not free: uncommitted changes"},
		{"resume without a conversation", command.Payload{Type: command.Resume, Session: "k"}, "", "resume needs a conversation"},
		{"push without a signature", command.Payload{Type: command.Push, Session: "z"}, "push /c/zynk", ""},
		{"push to an unknown session", command.Payload{Type: command.Push, Session: "nope"}, "", "no session nope"},
		{"answer the open question", command.Payload{Type: command.Answer, Session: "q", Question: "toolu_1", Text: "Colour: Blue"}, "answer q in conv to toolu_1: Colour: Blue", ""},
		{"answer a question no longer asked", command.Payload{Type: command.Answer, Session: "q", Question: "toolu_0", Text: "x"}, "", "quiz is no longer asking that question"},
		{"answer a session asking nothing", command.Payload{Type: command.Answer, Session: "k", Question: "toolu_1", Text: "x"}, "", "knip is no longer asking"},
		{"answer without text", command.Payload{Type: command.Answer, Session: "q", Question: "toolu_1"}, "", "needs the question and its text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, mem, acts := commandAgent(t, ph)
			if err := a.execute(context.Background(), ph.command("c1", tt.payload, a.Now())); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(acts.calls, "|"); got != tt.calls {
				t.Errorf("calls = %q, want %q", got, tt.calls)
			}
			res := mem.Updated(store.Commands, "c1")
			if tt.failure == "" {
				if res["status"] != command.Done {
					t.Fatalf("result = %v", res)
				}
			} else if res["status"] != command.Failed || !strings.Contains(res["error"].(string), tt.failure) {
				t.Fatalf("result = %v, want failure %q", res, tt.failure)
			}
			if tt.payload.Type == command.Dispatch && tt.failure == "" {
				if task := mem.Updated(store.Tasks, "t1"); task["status"] != store.TaskDispatched {
					t.Fatalf("task = %v", task)
				}
			}
		})
	}
}

func TestActingOnANotifiedSessionDismissesItEverywhere(t *testing.T) {
	ph := newPhone(t)
	a, _, _ := commandAgent(t, ph)
	n := &recordingNotifier{}
	a.Notify = n
	if err := a.notify(context.Background(), []notify.Note{{Session: "h", Title: "hamta", Body: "Finished"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.execute(context.Background(), ph.command("c1", command.Payload{Type: command.Stop, Session: "h"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(n.notes, "|"); got != "hamta: Finished|dismiss h" {
		t.Fatalf("notes = %v", got)
	}
}

func TestClearedSessionShowsItsNewConversationBeforeAoeReportsIt(t *testing.T) {
	ph := newPhone(t)
	c := &fakeCollector{replies: map[string]*session.Message{"old": {Text: "Done with the last task"}}}
	stale := session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, Transcript: session.Transcript{ID: "old"}}
	stale.SetExchange(c.Exchange(stale.Transcript))
	c.set(stale)
	mem := store.NewMemory()
	acts := &fakeActions{}
	a := newAgent(c, mem)
	a.Commands = &Commands{Act: acts, Keys: func() ([]command.Key, error) { return []command.Key{ph.key}, nil }, Origin: testOrigin, RPID: testRP}
	ctx := context.Background()
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	published := func() session.Snapshot {
		t.Helper()
		v, _ := mem.Get(store.Sessions, "k")
		return v.(session.Snapshot)
	}
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Clear, Session: "k"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if s := published(); s.Message != nil || !s.Cleared {
		t.Fatalf("after /clear the phone shows %+v", s)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if s := published(); s.Message != nil || !s.Cleared {
		t.Fatalf("a refresh while aoe reports the old conversation shows %+v", s)
	}
	if err := a.execute(ctx, ph.command("c2", command.Payload{Type: command.Clear, Session: "k"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(acts.calls, "|"); got != "clear k" {
		t.Fatalf("calls = %q, want a second clear to do nothing", got)
	}

	c.mu.Lock()
	c.replies["cleared-k"] = &session.Message{Text: "Started the new task"}
	c.mu.Unlock()
	caught := stale
	caught.Transcript.ID = "cleared-k"
	caught.SetExchange(c.Exchange(caught.Transcript))
	c.set(caught)
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if s := published(); s.Message == nil || s.Message.Text != "Started the new task" {
		t.Fatalf("once aoe reports the new conversation the phone shows %+v", s)
	}
	c.set(stale)
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if s := published(); s.Message == nil || s.Message.Text != "Done with the last task" {
		t.Fatalf("a clear followed after aoe caught up still overrides it: %+v", s)
	}
}

func TestDispatchWithClearPublishesTheNewConversation(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	if err := a.execute(context.Background(), ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x", Clear: true}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if a.sessions["k"].Transcript.ID != "cleared-k" {
		t.Fatalf("transcript = %+v", a.sessions["k"].Transcript)
	}
	if v, _ := mem.Get(store.Sessions, "k"); !v.(session.Snapshot).Cleared {
		t.Fatalf("published %+v", v)
	}
}

func TestUnsignedCommandsDoNothing(t *testing.T) {
	ph := newPhone(t)
	a, mem, acts := commandAgent(t, ph)
	in := ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x"}, a.Now())
	in.Command.Assertion = nil
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(acts.calls) != 0 || mem.Updated(store.Commands, "c1")["status"] != command.Failed {
		t.Fatalf("calls %v, result %v", acts.calls, mem.Updated(store.Commands, "c1"))
	}
	if mem.Updated(store.Tasks, "t1") != nil {
		t.Fatal("task marked dispatched by an unsigned command")
	}
}

func TestRunExecutesIncomingCommands(t *testing.T) {
	ph := newPhone(t)
	a, mem, acts := commandAgent(t, ph)
	cmds := make(chan store.Incoming, 1)
	cmds <- ph.command("c9", command.Payload{Type: command.Stop, Session: "k"}, a.Now())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx, nil, cmds) }()
	waitFor(t, func() bool { return mem.Updated(store.Commands, "c9")["status"] == command.Done })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Join(acts.calls, "|") != "stop k" {
		t.Fatalf("calls = %v", acts.calls)
	}
}

func TestDispatchedTaskIsMarkedDoneWhenItsSessionFinishes(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	_ = mem.Put(ctx, store.Tasks, "t1", store.Task{Project: "knip", Text: "Add --json", Status: store.TaskQueued})
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "Add --json"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t1"); got["session_id"] != "k" || got["conversation_id"] != "now" {
		t.Fatalf("task after dispatch = %v", got)
	}
	c := a.Collect.(*fakeCollector)
	later := a.Now().Add(time.Hour)

	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Working, Message: &session.Message{At: later}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t1")["status"]; got != store.TaskDispatched {
		t.Fatalf("working session finished the task: %v", got)
	}

	c.set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Unpushed,
		Message: &session.Message{At: later}, Git: &session.Git{Unpushed: 1, Last: &session.Commit{Hash: "3f2a1c4", At: later}}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	got := mem.Updated(store.Tasks, "t1")
	if got["status"] != store.TaskDone || got["commit"] != "3f2a1c4" {
		t.Fatalf("task = %v", got)
	}
}

func TestTaskRecordsTheConversationItWasSentInto(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	_ = mem.Put(ctx, store.Tasks, "t1", store.Task{Project: "knip", Text: "x", Status: store.TaskQueued})
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x", Clear: true}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t1")["conversation_id"]; got != "cleared-k" {
		t.Fatalf("conversation after clear = %v", got)
	}

	_ = mem.Put(ctx, store.Tasks, "t2", store.Task{Project: "sakku", Text: "Fix login", Status: store.TaskQueued})
	if err := a.execute(ctx, ph.command("c2", command.Payload{Type: command.Dispatch, Project: "sakku", Task: "t2", Text: "Fix login"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if got, ok := mem.Updated(store.Tasks, "t2")["conversation_id"]; ok {
		t.Fatalf("a new session's task has conversation %v before Claude Code starts", got)
	}
	later := a.Now().Add(time.Hour)
	a.Collect.(*fakeCollector).set(session.Snapshot{ID: "new-sakku", Title: "sakku", Path: "/c/sakku", State: session.Idle,
		Transcript: session.Transcript{ID: "first"}, Message: &session.Message{At: later}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t2"); got["status"] != store.TaskDone || got["conversation_id"] != "first" {
		t.Fatalf("task = %v", got)
	}
}

func TestResumeShowsTheResumedConversationAtOnce(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Resume, Session: "k", Conversation: "older"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	v, _ := mem.Get(store.Sessions, "k")
	if got := v.(session.Snapshot).Transcript.ID; got != "older" {
		t.Fatalf("published conversation = %q", got)
	}
}

type memHidden struct{ names []string }

func (m *memHidden) Load() ([]string, error)   { return m.names, nil }
func (m *memHidden) Save(names []string) error { m.names = names; return nil }

func TestHideAndShowProjects(t *testing.T) {
	ph := newPhone(t)
	ctx := context.Background()
	a, mem, acts := commandAgent(t, ph)
	saved := &memHidden{names: []string{"zynk"}}
	a.Hidden = saved
	a.hidden = nil
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	hidden := func(name string) bool {
		v, _ := mem.Get(store.Projects, name)
		return v.(session.Project).Hidden
	}
	if !hidden("zynk") || hidden("knip") {
		t.Fatal("the saved list is not published")
	}

	run := func(id string, p command.Payload) map[string]any {
		t.Helper()
		if err := a.execute(ctx, ph.command(id, p, a.Now())); err != nil {
			t.Fatal(err)
		}
		return mem.Updated(store.Commands, id)
	}
	if res := run("c1", command.Payload{Type: command.Hide, Projects: []string{"knip", "dorna"}}); res["status"] != command.Done {
		t.Fatalf("hide = %v", res)
	}
	if !hidden("knip") || !hidden("dorna") || strings.Join(saved.names, ",") != "dorna,knip,zynk" {
		t.Fatalf("after hide: saved %v", saved.names)
	}
	if res := run("c2", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x"}); res["status"] != command.Failed || !strings.Contains(res["error"].(string), "knip is hidden") {
		t.Fatalf("dispatch to a hidden project = %v", res)
	}
	if len(acts.calls) != 0 {
		t.Fatalf("calls = %v", acts.calls)
	}
	if res := run("c3", command.Payload{Type: command.Show, Projects: []string{"knip"}}); res["status"] != command.Done {
		t.Fatalf("show = %v", res)
	}
	if hidden("knip") || strings.Join(saved.names, ",") != "dorna,zynk" {
		t.Fatalf("after show: saved %v", saved.names)
	}
	// A refresh keeps what was hidden.
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !hidden("dorna") || hidden("knip") {
		t.Fatal("a refresh lost the hidden list")
	}
	if res := run("c4", command.Payload{Type: command.Hide, Projects: []string{"nope"}}); res["status"] != command.Failed || !strings.Contains(res["error"].(string), "no project named nope") {
		t.Fatalf("hide an unknown project = %v", res)
	}
	if res := run("c5", command.Payload{Type: command.Hide}); res["status"] != command.Failed {
		t.Fatalf("hide nothing = %v", res)
	}
}

func TestHideNeedsAStore(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	if err := a.execute(context.Background(), ph.command("c1", command.Payload{Type: command.Hide, Projects: []string{"knip"}}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed {
		t.Fatalf("result = %v", res)
	}
}

// lateReplyAgent has one session, knip, in conversation conv at message m1,
// and a reply to it that waited a day for the laptop.
func lateReplyAgent(t *testing.T) (*Agent, *fakeCollector, *store.Memory, *fakeActions, func(id, conversation, after string) store.Incoming) {
	t.Helper()
	ph := newPhone(t)
	c := &fakeCollector{replies: map[string]*session.Message{"conv": {ID: "m1", Text: "Done."}}}
	c.set(knipIn(c, "conv"))
	mem := store.NewMemory()
	acts := &fakeActions{}
	a := newAgent(c, mem)
	a.Commands = &Commands{Act: acts, Keys: func() ([]command.Key, error) { return []command.Key{ph.key}, nil }, Origin: testOrigin, RPID: testRP}
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	reply := func(id, conversation, after string) store.Incoming {
		signed := a.Now().Add(-24 * time.Hour)
		return ph.command(id, command.Payload{Type: command.Reply, Session: "k", Text: "go on",
			Conversation: conversation, After: after, Expires: signed.Add(time.Minute).UnixMilli()}, signed)
	}
	return a, c, mem, acts, reply
}

func knipIn(c *fakeCollector, conversation string) session.Snapshot {
	s := session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, Transcript: session.Transcript{ID: conversation}}
	c.Transcribe(&s)
	return s
}

func TestLateReplyIsSentWhileItsConversationStands(t *testing.T) {
	a, _, mem, acts, reply := lateReplyAgent(t)
	in := reply("c1", "conv", "m1")
	mem.Send(in)
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Done || strings.Join(acts.calls, "|") != "send k: go on" {
		t.Fatalf("status = %v, calls = %v", res, acts.calls)
	}
}

func TestLateReplyIsHeldOnceTheConversationMovedOn(t *testing.T) {
	a, _, mem, acts, reply := lateReplyAgent(t)
	in := reply("c1", "conv", "m0")
	mem.Send(in)
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Held || len(acts.calls) != 0 {
		t.Fatalf("status = %v, calls = %v", res, acts.calls)
	}
}

func TestLateReplyToAClearedConversationFails(t *testing.T) {
	a, _, mem, acts, reply := lateReplyAgent(t)
	in := reply("c1", "old", "m1")
	mem.Send(in)
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed || !strings.Contains(res["error"].(string), "cleared") || len(acts.calls) != 0 {
		t.Fatalf("status = %v, calls = %v", res, acts.calls)
	}
}

func TestHeldRepliesAreDroppedWhenTheirConversationIsCleared(t *testing.T) {
	ctx := context.Background()
	a, c, mem, _, reply := lateReplyAgent(t)
	in := reply("c1", "conv", "m0")
	mem.Send(in)
	if err := a.execute(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Held {
		t.Fatalf("an unchanged conversation dropped the held reply: %v", res)
	}
	c.set(knipIn(c, "new"))
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed || !strings.Contains(res["error"].(string), "cleared") {
		t.Fatalf("status = %v", res)
	}
}

func TestClearFromThePhoneDropsHeldReplies(t *testing.T) {
	ctx := context.Background()
	a, _, mem, _, reply := lateReplyAgent(t)
	in := reply("c1", "conv", "m0")
	mem.Send(in)
	if err := a.execute(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := a.cleared(ctx, "k", "cleared-k"); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed {
		t.Fatalf("status = %v", res)
	}
}
