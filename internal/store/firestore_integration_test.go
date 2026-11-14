//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/idea"
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

	v := idea.Idea{ID: "integration-round-trip", Title: "Emulator", CreatedAt: time.Now().UTC()}
	if err := s.Create(ctx, v); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	for _, candidate := range got {
		if candidate.ID == v.ID && candidate.Title == v.Title {
			return
		}
	}
	t.Fatalf("idea not found in %#v", got)
}

func TestFirestoreAdminKey(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("run with make test-integration")
	}
	ctx := context.Background()
	s, err := NewFirestore(ctx, "peyk-test")
	if err != nil {
		t.Fatalf("NewFirestore() error: %v", err)
	}
	defer s.Close()

	ref := s.client.Collection("config").Doc("admin")
	if _, err := ref.Delete(ctx); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, err := s.AdminKey(ctx); !errors.Is(err, ErrNoAdminKey) {
		t.Fatalf("AdminKey() without a document = %v, want ErrNoAdminKey", err)
	}
	if _, err := ref.Set(ctx, map[string]any{"key": "k1"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if got, err := s.AdminKey(ctx); err != nil || got != "k1" {
		t.Fatalf("AdminKey() = %q, %v", got, err)
	}
}
