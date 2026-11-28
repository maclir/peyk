package laptop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/idea"
)

func TestProjectLensesReadsCachesAndInvalidatesOnChange(t *testing.T) {
	dir := t.TempDir()
	var pl ProjectLenses
	if got := pl.Find(dir); got != nil {
		t.Fatalf("missing file = %+v, want nil", got)
	}

	peykDir := filepath.Join(dir, ".peyk")
	if err := os.MkdirAll(peykDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(peykDir, "lenses.md")
	write := func(body string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, mod, mod); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	write("## Invariants\nCheck invariants.\n", t0)
	want1 := []idea.Lens{{ID: "p:invariants", Name: "Invariants", Ask: "Check invariants."}}
	got1 := pl.Find(dir)
	if !reflect.DeepEqual(got1, want1) {
		t.Fatalf("first read = %+v, want %+v", got1, want1)
	}
	// Mutating the returned slice must not corrupt the cache.
	got1[0].Name = "mutated"
	if got2 := pl.Find(dir); !reflect.DeepEqual(got2, want1) {
		t.Fatalf("cached read = %+v, want %+v", got2, want1)
	}

	write("## Invariants\nCheck invariants.\n\n## Offline\nCheck offline sync.\n", t0.Add(time.Second))
	want2 := []idea.Lens{
		{ID: "p:invariants", Name: "Invariants", Ask: "Check invariants."},
		{ID: "p:offline", Name: "Offline", Ask: "Check offline sync."},
	}
	if got3 := pl.Find(dir); !reflect.DeepEqual(got3, want2) {
		t.Fatalf("updated read = %+v, want %+v", got3, want2)
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got4 := pl.Find(dir); got4 != nil {
		t.Fatalf("after delete = %+v, want nil", got4)
	}
}
