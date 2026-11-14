package session

import "testing"

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
