package session

import (
	"testing"
	"time"
)

func TestDerive(t *testing.T) {
	clean := &Git{}
	tests := []struct {
		name   string
		status string
		git    *Git
		want   State
	}{
		{"waiting beats dirty tree", "waiting", &Git{Dirty: 3}, NeedsInput},
		{"running agent with uncommitted work", "running", &Git{Dirty: 2}, Working},
		{"starting counts as working", "starting", clean, Working},
		{"error", "error", clean, Error},
		{"idle with uncommitted changes", "idle", &Git{Dirty: 1, Unpushed: 2}, Dirty},
		{"idle with unpushed commits", "idle", &Git{Unpushed: 2}, Unpushed},
		{"stopped with unpushed commits", "stopped", &Git{Unpushed: 1}, Unpushed},
		{"idle and clean", "idle", clean, Idle},
		{"stopped and clean", "stopped", clean, Stopped},
		{"no repository", "idle", nil, Idle},
		{"unknown status", "unknown", clean, Idle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Derive(tt.status, tt.git); got != tt.want {
				t.Fatalf("Derive(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestRankPutsAttentionFirst(t *testing.T) {
	order := []State{NeedsInput, Error, Dirty, Unpushed, Working, Idle, Stopped}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() >= order[i].Rank() {
			t.Fatalf("%s should rank before %s", order[i-1], order[i])
		}
	}
	if State("other").Rank() <= Stopped.Rank() {
		t.Fatal("unknown states should sort last")
	}
}

func TestProjectsFromSessionsAndRepositories(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	snaps := []Snapshot{
		{ID: "k", Title: "knip", Path: "/c/knip", State: Unpushed, Git: &Git{Unpushed: 2, Last: &Commit{At: at}}},
		{ID: "m", Title: "maclir.net", Path: "/c/maclir", State: Idle, Message: &Message{At: at.Add(time.Hour)}},
		{ID: "w", Title: "hamta", Path: "/c/hamta", State: Working},
	}
	got := Projects(snaps, []string{"/c/knip", "/c/maclir", "/c/sakku"})
	want := []Project{
		{Name: "knip", Path: "/c/knip", SessionID: "k", Busy: "2 unpushed commits", Active: at},
		{Name: "maclir.net", Path: "/c/maclir", SessionID: "m", Free: true, Active: at.Add(time.Hour)},
		{Name: "hamta", Path: "/c/hamta", SessionID: "w", Busy: "working"},
		{Name: "sakku", Path: "/c/sakku", Free: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d projects: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("project %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
