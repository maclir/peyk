//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/session"
)

func TestFirestoreRoundTrip(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("run with make test-integration")
	}
	ctx := context.Background()
	s, err := NewFirestore(ctx, "peyk-test")
	if err != nil {
		t.Fatalf("NewFirestore() error: %v", err)
	}
	defer s.Close()

	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	snap := session.Snapshot{
		ID: "it-knip", Title: "knip", State: session.Unpushed, AoeStatus: "idle",
		Git:     &session.Git{Branch: "main", Upstream: "origin/main", Unpushed: 2, Last: &session.Commit{Hash: "3f2a1c4", Subject: "Ship", At: at}},
		Message: &session.Message{Text: "Done.\n\n- one", At: at},
	}
	if err := s.Put(ctx, Sessions, snap.ID, snap); err != nil {
		t.Fatal(err)
	}
	doc, err := s.client.Collection("sessions").Doc("it-knip").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got session.Snapshot
	if err := doc.DataTo(&got); err != nil {
		t.Fatal(err)
	}
	if got.Git.Unpushed != 2 || got.Message.Text != snap.Message.Text || got.State != session.Unpushed {
		t.Fatalf("round trip: %+v", got)
	}
	ids, err := s.IDs(ctx, Sessions)
	if err != nil || !contains(ids, "it-knip") {
		t.Fatalf("SessionIDs = %v, %v", ids, err)
	}
	if err := s.Delete(ctx, Sessions, "it-knip"); err != nil {
		t.Fatal(err)
	}
	if ids, _ = s.IDs(ctx, Sessions); contains(ids, "it-knip") {
		t.Fatal("session not deleted")
	}
	if err := s.SetLaptop(ctx, Laptop{Online: true, SeenAt: at}); err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestFirestoreCommandsAndPairing(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("run with make test-integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewFirestore(ctx, "peyk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.Put(ctx, Commands, "it-done", command.Command{Type: command.Push, Payload: "{}", Status: command.Done})
	if err := s.Put(ctx, Commands, "it-pending", command.Command{Type: command.Push, Payload: `{"id":"it-pending"}`, Status: command.Pending}); err != nil {
		t.Fatal(err)
	}
	listenCtx, stop := context.WithCancel(ctx)
	out := make(chan Incoming)
	errs := make(chan error, 1)
	go func() { errs <- s.Commands(listenCtx, out) }()
	select {
	case in := <-out:
		if in.ID != "it-pending" || in.Command.Type != command.Push {
			t.Fatalf("got %+v", in)
		}
	case <-ctx.Done():
		t.Fatal("pending command not delivered")
	}
	if err := s.Update(ctx, Commands, "it-pending", map[string]any{"status": command.Done}); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}

	got := make(chan Pairing, 1)
	go func() {
		p, err := s.Pairing(ctx, "it-code-abcdefghijklmnop")
		if err != nil {
			t.Error(err)
		}
		got <- p
	}()
	time.Sleep(200 * time.Millisecond)
	if err := s.Put(ctx, Pairings, "it-code-abcdefghijklmnop", Pairing{CredentialID: "c", PublicKey: "k", Alg: -7}); err != nil {
		t.Fatal(err)
	}
	if p := <-got; p.CredentialID != "c" || p.Alg != -7 {
		t.Fatalf("pairing = %+v", p)
	}
}

func TestFirestoreDeliversWaitingRepliesInOrderAndListsHeldOnes(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("run with make test-integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewFirestore(ctx, "peyk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	// Ids sort the other way round from when the replies were written.
	for i, id := range []string{"it-order-c", "it-order-b", "it-order-a"} {
		c := command.Command{Type: command.Reply, Payload: `{}`, Status: command.Pending, CreatedAt: at.Add(time.Duration(i) * time.Minute)}
		if err := s.Put(ctx, Commands, id, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, Commands, "it-held", command.Command{Type: command.Reply, Payload: `{"session":"k"}`, Status: command.Held, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	listenCtx, stop := context.WithCancel(ctx)
	out := make(chan Incoming)
	errs := make(chan error, 1)
	go func() { errs <- s.Commands(listenCtx, out) }()
	var order []string
	for len(order) < 3 {
		select {
		case in := <-out:
			if len(in.ID) > 9 && in.ID[:9] == "it-order-" {
				order = append(order, in.ID)
			}
		case <-ctx.Done():
			t.Fatalf("delivered only %v", order)
		}
	}
	if order[0] != "it-order-c" || order[1] != "it-order-b" || order[2] != "it-order-a" {
		t.Fatalf("order = %v", order)
	}
	for _, id := range order {
		if err := s.Update(ctx, Commands, id, map[string]any{"status": command.Done}); err != nil {
			t.Fatal(err)
		}
	}
	stop()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}

	held, err := s.HeldReplies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := held["it-held"]; !ok || c.Payload != `{"session":"k"}` {
		t.Fatalf("held = %+v", held)
	}
	for id := range held {
		if id != "it-held" {
			t.Fatalf("held lists %s", id)
		}
	}
}

func TestFirestoreHistoryRequests(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("run with make test-integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewFirestore(ctx, "peyk-test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	at := time.Date(2026, 11, 14, 9, 0, 0, 123456000, time.UTC)
	if err := s.Put(ctx, History, "it-history", map[string]any{"requested_at": at}); err != nil {
		t.Fatal(err)
	}
	out := make(chan HistoryRequest, 4)
	go s.HistoryRequests(ctx, out)
	r := <-out
	if r.Session != "it-history" || !r.Requested.Equal(at) || !r.Pending() {
		t.Fatalf("request = %+v", r)
	}
	entries := []session.Entry{{Kind: session.EntryPrompt, Text: "hi", At: at}}
	if err := s.Update(ctx, History, "it-history", map[string]any{"entries": entries, "served_at": r.Requested}); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Second)
	if err := s.Update(ctx, History, "it-history", map[string]any{"requested_at": later}); err != nil {
		t.Fatal(err)
	}
	// The answer itself is not pending, so the next request is the new one.
	if r := <-out; !r.Requested.Equal(later) || !r.Served.Equal(at) {
		t.Fatalf("next request = %+v", r)
	}
	if err := s.Delete(ctx, History, "it-history"); err != nil {
		t.Fatal(err)
	}
}
