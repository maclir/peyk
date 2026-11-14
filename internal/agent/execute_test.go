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
func (f *fakeActions) Stop(_ context.Context, s string) error {
	f.calls = append(f.calls, "stop "+s)
	return nil
}
func (f *fakeActions) Push(_ context.Context, dir string) error {
	f.calls = append(f.calls, "push "+dir)
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
	p.Expires = now.Add(time.Minute).UnixMilli()
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
		session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle},
		session.Snapshot{ID: "h", Title: "hamta", Path: "/c/hamta", State: session.Working},
		session.Snapshot{ID: "z", Title: "zynk", Path: "/c/zynk", State: session.Unpushed, Git: &session.Git{Unpushed: 1}},
		session.Snapshot{ID: "d", Title: "dorna", Path: "/c/dorna", State: session.Dirty, Git: &session.Git{Dirty: 2}},
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
		{"dispatch to a repository without a session", command.Payload{Type: command.Dispatch, Project: "sakku", Task: "t1", Text: "Fix login"}, "create sakku in /c/sakku|send new-sakku: Fix login", ""},
		{"dispatch to a busy session", command.Payload{Type: command.Dispatch, Project: "hamta", Task: "t1", Text: "x"}, "", "hamta is not free: working"},
		{"dispatch to a session with only unpushed commits", command.Payload{Type: command.Dispatch, Project: "zynk", Task: "t1", Text: "x"}, "send z: x", ""},
		{"dispatch to a session with uncommitted changes", command.Payload{Type: command.Dispatch, Project: "dorna", Task: "t1", Text: "x"}, "", "dorna is not free: uncommitted changes"},
		{"dispatch to an unknown project", command.Payload{Type: command.Dispatch, Project: "nope", Task: "t1", Text: "x"}, "", "no project named nope"},
		{"reply to a working session", command.Payload{Type: command.Reply, Session: "h", Text: "Only the current filter"}, "send h: Only the current filter", ""},
		{"stop", command.Payload{Type: command.Stop, Session: "h"}, "stop h", ""},
		{"push without a signature", command.Payload{Type: command.Push, Session: "z"}, "push /c/zynk", ""},
		{"push to an unknown session", command.Payload{Type: command.Push, Session: "q"}, "", "no session q"},
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
	if got := mem.Updated(store.Tasks, "t1"); got["session_id"] != "k" {
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
