package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

func TestDispatchAnIdeationTaskAsksForIdeasAndNamesKnownOnes(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, acts := commandAgent(t, ph)
	_ = mem.Put(ctx, store.Ideas, "old-1", idea.Idea{Project: "knip", Title: "Two date formatters", Status: idea.Open})
	_ = mem.Put(ctx, store.Ideas, "old-2", idea.Idea{Project: "knip", Title: "Three confirms", Status: idea.Open})
	_ = mem.Update(ctx, store.Ideas, "old-2", map[string]any{"status": idea.Dismissed, "note": "fine as is"})
	_ = mem.Put(ctx, store.Ideas, "other-1", idea.Idea{Project: "zynk", Title: "Not this project", Status: idea.Open})

	ask := &idea.Ask{Lenses: []string{"reuse"}, Max: 3}
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "The worker page", Ideate: ask}, a.Now())); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Done {
		t.Fatalf("result = %v", res)
	}
	sent := strings.Join(acts.calls, "|")
	for _, want := range []string{"send k: The worker page\n\nThis is an ideation task", "- Two date formatters\n", "- Three confirms (dismissed: fine as is)\n"} {
		if !strings.Contains(sent, want) {
			t.Errorf("sent lacks %q:\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "Not this project") {
		t.Errorf("sent names another project's idea:\n%s", sent)
	}
}

func TestDispatchRefusesABadIdeationAsk(t *testing.T) {
	ph := newPhone(t)
	for name, tt := range map[string]struct {
		p       command.Payload
		failure string
	}{
		"prototype and ideas": {command.Payload{Prototype: session.PrototypeHTML, Ideate: &idea.Ask{Lenses: []string{"reuse"}, Max: 3}}, "a prototype or for ideas, not both"},
		"unknown lens":        {command.Payload{Ideate: &idea.Ask{Lenses: []string{"vibes"}, Max: 3}}, `no lens "vibes"`},
	} {
		t.Run(name, func(t *testing.T) {
			a, mem, acts := commandAgent(t, ph)
			p := tt.p
			p.Type, p.Project, p.Task, p.Text = command.Dispatch, "knip", "t1", "x"
			if err := a.execute(context.Background(), ph.command("c1", p, a.Now())); err != nil {
				t.Fatal(err)
			}
			if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Failed || !strings.Contains(res["error"].(string), tt.failure) {
				t.Fatalf("result = %v, want %q", res, tt.failure)
			}
			if len(acts.calls) != 0 {
				t.Fatalf("calls = %v", acts.calls)
			}
		})
	}
}

func TestFinishedIdeationTaskFilesTheIdeasItsRunReported(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	ask := &idea.Ask{Lenses: []string{"reuse", "security"}, Max: 3}
	_ = mem.Put(ctx, store.Tasks, "t1", store.Task{Project: "knip", Text: "x", Status: store.TaskQueued, Ideate: ask})
	_ = mem.Put(ctx, store.Ideas, "old-1", idea.Idea{Project: "knip", Title: "Already known", Status: idea.Dismissed})
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x", Ideate: ask}, a.Now())); err != nil {
		t.Fatal(err)
	}
	later := a.Now().Add(time.Hour)
	a.Collect.(*fakeCollector).set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, Message: &session.Message{At: later},
		Ideas: []idea.Draft{
			{Lens: "reuse", Title: "Two formatters", Why: "w", Refs: []string{"a.js:1"}, Task: "Merge them"},
			{Lens: "security", Title: "already known", Task: "x"},
			{Lens: "bugs", Title: "Not asked for", Task: "x"},
		}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t1"); got["status"] != store.TaskDone || got["ideas"] != 1 {
		t.Fatalf("task = %v", got)
	}
	v, ok := mem.Get(store.Ideas, "t1-1")
	if !ok {
		t.Fatal("no idea filed")
	}
	got := v.(idea.Idea)
	if got.Project != "knip" || got.Task != "t1" || got.Title != "Two formatters" || got.Prompt != "Merge them" || got.Status != idea.Open || got.CreatedAt.IsZero() {
		t.Fatalf("idea = %+v", got)
	}
	if _, ok := mem.Get(store.Ideas, "t1-2"); ok {
		t.Fatal("filed an idea the run was not asked for or already knew")
	}
}

func TestFinishedOrdinaryTaskFilesNoIdeas(t *testing.T) {
	ctx := context.Background()
	ph := newPhone(t)
	a, mem, _ := commandAgent(t, ph)
	_ = mem.Put(ctx, store.Tasks, "t1", store.Task{Project: "knip", Text: "x", Status: store.TaskQueued})
	if err := a.execute(ctx, ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x"}, a.Now())); err != nil {
		t.Fatal(err)
	}
	a.Collect.(*fakeCollector).set(session.Snapshot{ID: "k", Title: "knip", Path: "/c/knip", State: session.Idle, Message: &session.Message{At: a.Now().Add(time.Hour)},
		Ideas: []idea.Draft{{Lens: "reuse", Title: "Stray", Task: "x"}}})
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mem.Updated(store.Tasks, "t1"); got["status"] != store.TaskDone || got["ideas"] != nil {
		t.Fatalf("task = %v", got)
	}
	if ids, _ := mem.IDs(ctx, store.Ideas); len(ids) != 0 {
		t.Fatalf("ideas = %v", ids)
	}
}
