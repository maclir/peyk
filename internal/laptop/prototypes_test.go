package laptop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

func published(at, url, title, version string) string {
	return `{"type":"user","timestamp":"` + at + `","toolUseResult":{"url":"` + url + `","path":"/tmp/p.html","artifact_id":"a-` + url[len(url)-1:] + `","title":"` + title + `","version":"` + version + `"}}`
}

func TestPrototypesFindsPublishedPagesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	write := func(lines ...string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, l := range lines {
			if _, err := f.WriteString(l + "\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(
		published("2026-11-14T08:00:00Z", "https://claude.ai/artifact/A", "First draft", "1"),
		// A read of someone else's page carries no version.
		`{"type":"user","timestamp":"2026-11-14T08:01:00Z","toolUseResult":{"url":"https://claude.ai/artifact/R","artifact_id":"r","title":"Read"}}`,
		`{"type":"user","timestamp":"2026-11-14T08:02:00Z","toolUseResult":{"stdout":"artifact_id","stderr":""}}`,
		published("2026-11-14T08:03:00Z", "https://evil.example/artifact/E", "Elsewhere", "1"),
	)
	p := &Prototypes{}
	tr := session.Transcript{Dir: dir, ID: "s"}
	got := p.Find(tr)
	if len(got) != 1 || got[0].URL != "https://claude.ai/artifact/A" || got[0].Title != "First draft" {
		t.Fatalf("got %+v", got)
	}

	// Appended later: a second page, and a republish that renames the first.
	write(
		published("2026-11-14T09:00:00Z", "https://claude.ai/artifact/B", "Second", "1"),
		published("2026-11-14T09:30:00Z", "https://claude.ai/artifact/A", "Renamed", "2"),
	)
	got = p.Find(tr)
	if len(got) != 2 || got[0].Title != "Renamed" || got[1].URL != "https://claude.ai/artifact/B" {
		t.Fatalf("got %+v", got)
	}
	if !got[0].At.Equal(time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("at = %v", got[0].At)
	}

	// A line still being written is read once it is complete.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	line := published("2026-11-14T10:00:00Z", "https://claude.ai/artifact/C", "Third", "1")
	f.WriteString(line[:20])
	f.Close()
	if got := p.Find(tr); len(got) != 2 {
		t.Fatalf("a partial line yields nothing yet: %+v", got)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(line[20:] + "\n")
	f.Close()
	if got := p.Find(tr); len(got) != 3 || got[0].Title != "Third" {
		t.Fatalf("got %+v", got)
	}

	if got := p.Find(session.Transcript{Dir: dir}); got != nil {
		t.Fatalf("without its own transcript a session has no prototypes: %+v", got)
	}
}

func TestPrototypesTellPagesFromDesignsAndOtherTypes(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		published("2026-11-14T08:00:00Z", "https://claude.ai/artifact/H", "Page", "1"),
		`{"type":"user","timestamp":"2026-11-14T08:01:00Z","toolUseResult":{"url":"https://claude.ai/artifact/D","artifact_id":"d","title":"Canvas","version":"1","type":{"url":"https://claude.ai/artifact/QKN21svewxgyPb6SYRqWnd","release":"r"}}}`,
		`{"type":"user","timestamp":"2026-11-14T08:02:00Z","toolUseResult":{"url":"https://claude.ai/artifact/S","artifact_id":"s","title":"Deck","version":"1","type":{"url":"https://claude.ai/artifact/8jTsAFQMFDb2oA8MsPJ2eL"}}}`,
	}
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, p := range (&Prototypes{}).Find(session.Transcript{Dir: dir, ID: "s"}) {
		kinds[p.Title] = p.Kind
	}
	want := map[string]string{"Page": session.PrototypeHTML, "Canvas": session.PrototypeDesign, "Deck": session.PrototypeOther}
	for title, kind := range want {
		if kinds[title] != kind {
			t.Errorf("%s: kind %q, want %q", title, kinds[title], kind)
		}
	}
}
