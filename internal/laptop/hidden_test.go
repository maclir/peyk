package laptop

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHiddenFileRoundTrips(t *testing.T) {
	f := HiddenFile{Path: filepath.Join(t.TempDir(), "peyk", "hidden.json")}
	if got, err := f.Load(); err != nil || got != nil {
		t.Fatalf("missing file = %v, %v", got, err)
	}
	if err := f.Save([]string{"areh", "knip"}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Load(); err != nil || !slices.Equal(got, []string{"areh", "knip"}) {
		t.Fatalf("loaded %v, %v", got, err)
	}
	if err := f.Save(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Load(); err != nil || len(got) != 0 {
		t.Fatalf("after clearing = %v, %v", got, err)
	}
}

func TestHiddenFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hidden.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (HiddenFile{Path: path}).Load(); err == nil {
		t.Fatal("want an error for a corrupt file")
	}
}
