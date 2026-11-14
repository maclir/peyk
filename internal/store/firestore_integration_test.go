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
