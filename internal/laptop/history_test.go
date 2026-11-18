package laptop

import (
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

func TestHistoryReadsTheWholeConversation(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	writeTranscript(t, claude, work, "s", time.Now(),
		clearEntry("2026-11-14T08:59:00Z"),
		human("2026-11-14T09:00:00Z", `"Add a --json flag"`),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:05Z","message":{"content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Bash","input":{"command":"make   test","description":"Run tests"}}]}}`,
		`{"type":"user","timestamp":"2026-11-14T09:00:06Z","toolUseResult":{},"message":{"content":[{"type":"tool_result","content":"secret output"}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-14T09:00:07Z","message":{"content":[{"type":"text","text":"subagent"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-14T09:00:08Z","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"main.go","old_string":"a"}}]}}`,
		`{"type":"assistant","timestamp":"2026-11-14T09:00:09Z","message":{"content":[{"type":"tool_use","name":"TaskList","input":{}}]}}`,
		assistant("2026-11-14T09:01:00Z", "Added."),
	)
	got, truncated := History(session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"})
	at := func(sec int) time.Time { return time.Date(2026, 11, 14, 9, sec/60, sec%60, 0, time.UTC) }
	want := []session.Entry{
		{Kind: session.EntryPrompt, Text: "Add a --json flag", At: at(0)},
		{Kind: session.EntryReply, Text: "On it.", At: at(5)},
		{Kind: session.EntryTool, Tool: "Bash", Text: "make test", At: at(5)},
		{Kind: session.EntryTool, Tool: "Edit", Text: "main.go", At: at(8)},
		{Kind: session.EntryTool, Tool: "TaskList", At: at(9)},
		{Kind: session.EntryReply, Text: "Added.", At: at(60)},
		{Kind: session.EntryTool, Tool: "Bash", At: at(60)},
	}
	if truncated || len(got) != len(want) {
		t.Fatalf("truncated = %v, got %+v", truncated, got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Tool != want[i].Tool || got[i].Text != want[i].Text || !got[i].At.Equal(want[i].At) {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHistoryIncludesAPromptSentWhileWorking(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "s", time.Now(),
		human("2026-11-14T09:00:00Z", `"Fix the bug"`),
		queued("2026-11-14T09:00:30Z", "Also add a test"),
		`{"type":"attachment","timestamp":"2026-11-14T09:00:31Z","attachment":{"type":"queued_command","prompt":"from a hook","origin":{"kind":"hook"}}}`,
		assistant("2026-11-14T09:01:00Z", "Done."),
	)
	got, _ := History(session.Transcript{Dir: TranscriptDir(claude, "/w"), ID: "s"})
	var prompts []string
	for _, e := range got {
		if e.Kind == session.EntryPrompt {
			prompts = append(prompts, e.Text)
		}
	}
	if strings.Join(prompts, "|") != "Fix the bug|Also add a test" {
		t.Fatalf("prompts = %q", prompts)
	}
}

func queued(ts, text string) string {
	return `{"type":"attachment","isSidechain":false,"timestamp":"` + ts + `","attachment":{"type":"queued_command","prompt":` + quote(text) + `,"origin":{"kind":"human"}}}`
}

func TestHistoryShortensAToolLine(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "s", time.Now(),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"`+strings.Repeat("x", 500)+`"}}]}}`)
	got, _ := History(session.Transcript{Dir: TranscriptDir(claude, "/w"), ID: "s"})
	if len(got) != 1 || len([]rune(got[0].Text)) != maxToolLine || !strings.HasSuffix(got[0].Text, "…") {
		t.Fatalf("got %+v", got)
	}
}

func TestHistoryWithoutItsOwnTranscriptReadsTheNewest(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "old", time.Now().Add(-time.Hour), human("2026-11-14T08:00:00Z", `"old"`))
	writeTranscript(t, claude, "/w", "new", time.Now(), human("2026-11-14T09:00:00Z", `"new"`))
	got, _ := History(session.Transcript{Dir: TranscriptDir(claude, "/w")})
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("got %+v", got)
	}
	if got, _ := History(session.Transcript{Dir: "/nowhere", ID: "x"}); got != nil {
		t.Fatalf("missing transcript: %+v", got)
	}
}

func TestBoundKeepsTheNewestEntries(t *testing.T) {
	entries := []session.Entry{{Text: "aaaa"}, {Text: "bbbb"}, {Text: "cc", Tool: "Bash"}}
	if got, truncated := bound(entries, 10); !truncated || len(got) != 2 || got[0].Text != "bbbb" {
		t.Fatalf("got %+v, truncated %v", got, truncated)
	}
	if got, truncated := bound(entries, 14); truncated || len(got) != 3 {
		t.Fatalf("got %+v, truncated %v", got, truncated)
	}
}
