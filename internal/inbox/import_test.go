package inbox

import (
	"context"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

var projects = []session.Project{
	{Name: "maclir.net", Path: "/c/maclir"},
	{Name: "knip", Path: "/c/knip"},
}

func TestImportMatchesNamesAndDirectoriesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	in := `[
	  {"project":"Maclir","text":" Add RSS feed ","created_at":"2026-10-01T10:00:00Z"},
	  {"project":"knip","text":"Add --json output","created_at":"2026-11-01T10:00:00Z"},
	  {"project":"knip","text":"   ","created_at":"2026-11-01T10:00:00Z"}
	]`
	n, err := Import(ctx, strings.NewReader(in), projects, mem)
	if err != nil || n != 2 {
		t.Fatalf("Import = %d, %v", n, err)
	}
	ids, _ := mem.IDs(ctx, store.Tasks)
	if len(ids) != 2 {
		t.Fatalf("tasks = %v", ids)
	}
	v, _ := mem.Get(store.Tasks, ids[0])
	got := map[string]bool{}
	for _, id := range ids {
		v, _ = mem.Get(store.Tasks, id)
		task := v.(store.Task)
		if task.Status != store.TaskQueued || task.CreatedAt.IsZero() {
			t.Fatalf("task = %+v", task)
		}
		got[task.Project+":"+task.Text] = true
	}
	if !got["maclir.net:Add RSS feed"] || !got["knip:Add --json output"] {
		t.Fatalf("tasks = %v", got)
	}
	if _, err := Import(ctx, strings.NewReader(in), projects, mem); err != nil {
		t.Fatal(err)
	}
	if ids, _ = mem.IDs(ctx, store.Tasks); len(ids) != 2 {
		t.Fatalf("second import added tasks: %v", ids)
	}
}

func TestImportWritesNothingWhenAProjectIsUnknown(t *testing.T) {
	mem := store.NewMemory()
	in := `[{"project":"knip","text":"a"},{"project":"Groceries","text":"b"}]`
	_, err := Import(context.Background(), strings.NewReader(in), projects, mem)
	if err == nil || !strings.Contains(err.Error(), "Groceries") {
		t.Fatalf("err = %v", err)
	}
	if mem.Writes() != 0 {
		t.Fatal("partial import written")
	}
}
