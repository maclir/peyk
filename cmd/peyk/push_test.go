package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/store"
)

type fakeSender struct {
	gone map[string]bool
	fail error
	sent []string
}

func (f *fakeSender) Send(_ context.Context, sub notify.Subscription, n notify.Note) error {
	if f.gone[sub.ID] {
		return notify.ErrGone
	}
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, sub.ID+":"+n.Title)
	return nil
}

func TestNotifierDeletesGoneSubscriptionsAndFailsOnOtherErrors(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	_ = mem.Put(ctx, store.Subscriptions, "pixel", notify.Subscription{Endpoint: "https://a"})
	_ = mem.Put(ctx, store.Subscriptions, "old", notify.Subscription{Endpoint: "https://b"})
	s := &fakeSender{gone: map[string]bool{"old": true}}
	n := notifier{store: mem, sender: s}
	if err := n.Notify(ctx, []notify.Note{{Title: "knip finished"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.sent, ",") != "pixel:knip finished" {
		t.Fatalf("sent %v", s.sent)
	}
	if ids, _ := mem.IDs(ctx, store.Subscriptions); strings.Join(ids, ",") != "pixel" {
		t.Fatalf("subscriptions left: %v", ids)
	}
	s.fail = errors.New("429 Too Many Requests")
	if err := n.Notify(ctx, []notify.Note{{Title: "x"}}); err == nil {
		t.Fatal("a failed push must surface")
	}
}
