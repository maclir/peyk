//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

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
	if err := s.PutSession(ctx, snap); err != nil {
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
	ids, err := s.SessionIDs(ctx)
	if err != nil || !contains(ids, "it-knip") {
		t.Fatalf("SessionIDs = %v, %v", ids, err)
	}
	if err := s.DeleteSession(ctx, "it-knip"); err != nil {
		t.Fatal(err)
	}
	if ids, _ = s.SessionIDs(ctx); contains(ids, "it-knip") {
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
