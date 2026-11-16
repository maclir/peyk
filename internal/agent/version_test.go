package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

type fakeCheckout struct {
	calls    []string
	behind   int
	fetched  int
	updateTo error
}

func (f *fakeCheckout) Fetch(context.Context) error {
	f.calls = append(f.calls, "fetch")
	f.behind = f.fetched
	return nil
}

func (f *fakeCheckout) Version(context.Context) (session.Version, error) {
	return session.Version{Commit: "abc1234", Behind: f.behind}, nil
}

func (f *fakeCheckout) Update(context.Context) error {
	f.calls = append(f.calls, "update")
	if f.updateTo != nil {
		return f.updateTo
	}
	f.behind = 0
	return nil
}

func TestRefreshPublishesTheVersionWithoutFetching(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	co := &fakeCheckout{behind: 1, fetched: 3}
	a.Checkout = co
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, _ := mem.Get("laptop", "version"); v.(session.Version).Behind != 1 || len(co.calls) != 0 {
		t.Fatalf("version %+v, calls %v", v, co.calls)
	}
	writes := mem.Writes()
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mem.Writes() != writes {
		t.Fatal("an unchanged version was rewritten")
	}
}

func TestCheckFetchesWithoutASignatureAndPublishesWhatArrived(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	co := &fakeCheckout{fetched: 2}
	a.Checkout = co
	in := ph.command("c1", command.Payload{Type: command.CheckUpdate}, a.Now())
	if in.Command.Assertion != nil {
		t.Fatal("check should be sent unsigned")
	}
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Done {
		t.Fatalf("result %v", res)
	}
	if v, _ := mem.Get("laptop", "version"); v.(session.Version).Behind != 2 || strings.Join(co.calls, ",") != "fetch" || a.restart {
		t.Fatalf("version %+v, calls %v, restart %v", v, co.calls, a.restart)
	}
}

func TestUpdateNeedsASignature(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	co := &fakeCheckout{fetched: 2}
	a.Checkout = co
	in := ph.command("c1", command.Payload{Type: command.Update}, a.Now())
	in.Command.Assertion = nil
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if mem.Updated(store.Commands, "c1")["status"] != command.Failed || len(co.calls) != 0 || a.restart {
		t.Fatalf("unsigned update ran: calls %v", co.calls)
	}
}

func TestAFailedUpdateKeepsTheAgentRunning(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	a.Checkout = &fakeCheckout{fetched: 2, updateTo: errors.New("Not possible to fast-forward")}
	if err := a.execute(context.Background(), ph.command("c1", command.Payload{Type: command.Update}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed || !strings.Contains(res["error"].(string), "fast-forward") || a.restart {
		t.Fatalf("result %v, restart %v", res, a.restart)
	}
}

func TestRunEndsAfterAnUpdateSoLaunchdStartsTheNewBuild(t *testing.T) {
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	co := &fakeCheckout{fetched: 2}
	a.Checkout = co
	cmds := make(chan store.Incoming, 1)
	cmds <- ph.command("c1", command.Payload{Type: command.Update}, a.Now())
	done := make(chan error)
	go func() { done <- a.Run(context.Background(), nil, cmds) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept going after an update")
	}
	if mem.Updated(store.Commands, "c1")["status"] != command.Done || strings.Join(co.calls, ",") != "fetch,update" {
		t.Fatalf("result %v, calls %v", mem.Updated(store.Commands, "c1"), co.calls)
	}
	if v, _ := mem.Get("laptop", "version"); v.(session.Version).Behind != 0 {
		t.Fatalf("version after update %+v", v)
	}
}
