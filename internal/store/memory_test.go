package store

import (
	"context"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/idea"
)

func TestMemoryImplementsStoreAndListsNewestFirst(t *testing.T) {
	var s Store = NewMemory()
	ctx := context.Background()
	for _, v := range []idea.Idea{
		{ID: "old", Title: "Old", CreatedAt: time.Unix(1, 0)},
		{ID: "new", Title: "New", CreatedAt: time.Unix(2, 0)},
	} {
		if err := s.Create(ctx, v); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("List() = %#v", got)
	}
}
