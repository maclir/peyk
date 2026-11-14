package idea

import (
	"errors"
	"testing"
	"time"
)

func TestNewNormalizesAnIdea(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.FixedZone("test", 3600))
	got, err := New("one", "  Build something useful  ", now)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if got.Title != "Build something useful" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
}

func TestNewRejectsBlankTitle(t *testing.T) {
	_, err := New("one", " \n ", time.Now())
	if !errors.Is(err, ErrBlankTitle) {
		t.Fatalf("New() error = %v, want ErrBlankTitle", err)
	}
}
