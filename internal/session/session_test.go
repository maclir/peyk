package session

import (
	"slices"
	"testing"
	"time"
)

func TestDerive(t *testing.T) {
	clean := &Git{}
	tests := []struct {
		name         string
		status       string
		backgrounded bool
		git          *Git
		want         State
	}{
		{"waiting beats dirty tree", "waiting", false, &Git{Dirty: 3}, NeedsInput},
		{"running agent with uncommitted work", "running", false, &Git{Dirty: 2}, Working},
		{"starting counts as working", "starting", false, clean, Working},
		{"error", "error", false, clean, Error},
		{"idle with uncommitted changes", "idle", false, &Git{Dirty: 1, Unpushed: 2}, Dirty},
		{"idle with unpushed commits", "idle", false, &Git{Unpushed: 2}, Unpushed},
		{"stopped with unpushed commits", "stopped", false, &Git{Unpushed: 1}, Unpushed},
		{"idle and clean", "idle", false, clean, Idle},
		{"stopped and clean", "stopped", false, clean, Stopped},
		{"no repository", "idle", false, nil, Idle},
		{"unknown status", "unknown", false, clean, Idle},
		{"idle with a background task and unpushed commits", "idle", true, &Git{Unpushed: 1}, Working},
		{"idle with a background task and a dirty tree", "idle", true, &Git{Dirty: 1}, Working},
		{"waiting beats a background task", "waiting", true, clean, NeedsInput},
		{"stopped session's background task died with it", "stopped", true, &Git{Unpushed: 1}, Unpushed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Derive(tt.status, tt.backgrounded, tt.git); got != tt.want {
				t.Fatalf("Derive(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestRankPutsAttentionFirst(t *testing.T) {
	order := []State{NeedsInput, Error, Dirty, Unpushed, Idle, Working, Stopped}
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
		{ID: "d", Title: "dorna", Path: "/c/dorna", State: Dirty, Git: &Git{Dirty: 1}},
	}
	got := Projects(snaps, []string{"/c/knip", "/c/maclir", "/c/sakku"})
	want := []Project{
		{Name: "knip", Path: "/c/knip", SessionID: "k", Free: true, Active: at},
		{Name: "maclir.net", Path: "/c/maclir", SessionID: "m", Free: true, Active: at.Add(time.Hour)},
		{Name: "hamta", Path: "/c/hamta", SessionID: "w", Busy: "working"},
		{Name: "dorna", Path: "/c/dorna", SessionID: "d", Busy: "uncommitted changes"},
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

func TestFinished(t *testing.T) {
	sent := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	after, before := sent.Add(time.Minute), sent.Add(-time.Minute)
	reply := func(at time.Time) *Message { return &Message{Text: "done", At: at} }
	tests := []struct {
		name   string
		s      Snapshot
		done   bool
		commit string
	}{
		{"replied and committed", Snapshot{State: Unpushed, Message: reply(after), Git: &Git{Last: &Commit{Hash: "abc", At: after}}}, true, "abc"},
		{"replied without committing", Snapshot{State: Idle, Message: reply(after), Git: &Git{Last: &Commit{Hash: "old", At: before}}}, true, ""},
		{"stopped after replying", Snapshot{State: Stopped, Message: reply(after)}, true, ""},
		{"still working", Snapshot{State: Working, Message: reply(after)}, false, ""},
		{"asked a question", Snapshot{State: NeedsInput, Message: reply(after)}, false, ""},
		{"left uncommitted work", Snapshot{State: Dirty, Message: reply(after)}, false, ""},
		{"no reply since sending", Snapshot{State: Idle, Message: reply(before)}, false, ""},
		{"no reply at all", Snapshot{State: Idle}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, commit := Finished(tt.s, sent)
			if done != tt.done || commit != tt.commit {
				t.Fatalf("Finished = %v %q, want %v %q", done, commit, tt.done, tt.commit)
			}
		})
	}
}

func TestLatestIsTheNewerOfReplyAndPrompt(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	reply := &Message{ID: "r", At: at}
	later := &Message{ID: "p", At: at.Add(time.Minute)}
	earlier := &Message{ID: "p", At: at.Add(-time.Minute)}
	tests := []struct {
		reply, prompt *Message
		want          string
	}{
		{nil, nil, ""},
		{reply, nil, "r"},
		{nil, later, "p"},
		{reply, earlier, "r"},
		{reply, later, "p"},
	}
	for _, tt := range tests {
		var s Snapshot
		s.SetExchange(tt.reply, tt.prompt)
		if s.Latest != tt.want {
			t.Errorf("SetExchange(%+v, %+v).Latest = %q, want %q", tt.reply, tt.prompt, s.Latest, tt.want)
		}
	}
}

func TestCompareOrdersByStateThenRecentActivityThenTitle(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 17, 12, m, 0, 0, time.UTC) }
	msg := func(m int) *Message { return &Message{At: at(m)} }
	commit := func(m int) *Git { return &Git{Last: &Commit{At: at(m)}} }
	in := []Snapshot{
		{Title: "old question", State: NeedsInput, Message: msg(1)},
		{Title: "dirty", State: Dirty, Message: msg(9)},
		{Title: "new question", State: NeedsInput, Message: msg(1), Prompt: msg(5)},
		{Title: "old commit", State: Unpushed, Message: msg(9), Git: commit(1)},
		{Title: "new commit", State: Unpushed, Message: msg(0), Git: commit(2)},
		{Title: "b", State: Idle},
		{Title: "a", State: Idle},
		{Title: "recent idle", State: Idle, Message: msg(3)},
	}
	slices.SortStableFunc(in, Compare)
	var got []string
	for _, s := range in {
		got = append(got, s.Title)
	}
	want := []string{"new question", "old question", "dirty", "new commit", "old commit", "recent idle", "a", "b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSetExchangeTakesAnIdeationRunsIdeasOutOfItsReply(t *testing.T) {
	var s Snapshot
	s.SetExchange(&Message{ID: "r", Text: "Found one.\n\n```peyk-ideas\n[{\"lens\":\"reuse\",\"title\":\"T\",\"task\":\"x\"}]\n```"}, nil)
	if s.Message.Text != "Found one." || len(s.Ideas) != 1 || s.Ideas[0].Title != "T" {
		t.Fatalf("message = %q, ideas = %+v", s.Message.Text, s.Ideas)
	}
	s.SetExchange(&Message{ID: "r2", Text: "Plain"}, nil)
	if s.Message.Text != "Plain" || s.Ideas != nil {
		t.Fatalf("message = %q, ideas = %+v", s.Message.Text, s.Ideas)
	}
}

func TestTranscriptWithIDUpdatesAntigravityDir(t *testing.T) {
	claude := Transcript{Dir: "/c/projects/-w", ID: "old"}.WithID("new")
	if claude.ID != "new" || claude.Dir != "/c/projects/-w" {
		t.Fatalf("claude = %+v", claude)
	}
	agy := Transcript{Tool: Antigravity, AppDir: "/a", Dir: "/a/brain/old/.system_generated/logs", ID: "old"}.WithID("new")
	if agy.ID != "new" || agy.Dir != "/a/brain/new/.system_generated/logs" {
		t.Fatalf("antigravity = %+v", agy)
	}
	if empty := agy.WithID(""); empty.ID != "" || empty.Dir != "" {
		t.Fatalf("cleared antigravity = %+v", empty)
	}
}
