package laptop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// writeConversation lays out an Antigravity conversation the way the CLI
// does, and records it as the working directory's latest.
func writeConversation(t *testing.T, app, workdir, id string, lines, full []string) session.Transcript {
	t.Helper()
	logs := filepath.Join(app, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "transcript.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if full != nil {
		if err := os.WriteFile(filepath.Join(logs, "transcript_full.jsonl"), []byte(strings.Join(full, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setLatest(t, app, workdir, id)
	return AntigravityTranscript(app, workdir, "")
}

func setLatest(t *testing.T, app, workdir, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(app, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := `{"` + workdir + `": "` + id + `"}`
	if err := os.WriteFile(filepath.Join(app, "cache", "last_conversations.json"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	agyPrompt = `{"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-09-10T09:56:17Z", "content": "<USER_REQUEST>\nShould we move to 3.8 Flash?\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-10T11:56:17+02:00.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nThe user changed setting ` + "`Model Selection`" + ` from None to Gemini 3.8 Flash (High). No need to comment on this change.\n</USER_SETTINGS_CHANGE>"}`
	agyTool   = `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T09:56:20Z", "thinking": "**Looking**", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"grep -r gemini .\"", "toolSummary": "\"Finding Gemini callers\""}}]}`
	agyResult = `{"step_index": 2, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-09-10T09:56:21Z", "content": "Created At: 2026-09-10T11:56:21+02:00\nOutput"}`
	agyReply  = `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T09:57:00Z", "content": "### Short answer\n\nYes, for", "truncated_fields": ["content"]}`
	agyFull   = `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T09:57:00Z", "content": "### Short answer\n\nYes, for four of five projects."}`
)

func TestAntigravityExchangeReadsThePromptTheReplyAndTheModelPicked(t *testing.T) {
	app := t.TempDir()
	tr := writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt, agyTool, agyResult, agyReply}, []string{agyFull})
	if tr.ID != "conv-1" || tr.Tool != session.Antigravity {
		t.Fatalf("transcript = %+v, want the directory's latest Antigravity conversation", tr)
	}
	reply, prompt := LastExchange(tr)
	if prompt == nil || prompt.Text != "Should we move to 3.8 Flash?" || prompt.ID != "0" {
		t.Fatalf("prompt = %+v, want only what was typed", prompt)
	}
	if reply == nil || reply.Text != "### Short answer\n\nYes, for four of five projects." || reply.ID != "3" {
		t.Fatalf("reply = %+v, want the whole reply from transcript_full.jsonl", reply)
	}
	if reply.Model != "Gemini 3.8 Flash (High)" || !reply.At.Equal(time.Date(2026, 9, 10, 9, 57, 0, 0, time.UTC)) {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestAntigravityHistoryListsPromptsToolCallsAndReplies(t *testing.T) {
	app := t.TempDir()
	tr := writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt, agyTool, agyResult, agyReply}, []string{agyFull})
	entries, truncated := History(tr)
	if truncated || len(entries) != 3 {
		t.Fatalf("entries = %+v, truncated = %v", entries, truncated)
	}
	if e := entries[1]; e.Kind != session.EntryTool || e.Tool != "run_command" || e.Text != "Finding Gemini callers" {
		t.Fatalf("tool entry = %+v, want its summary", e)
	}
	if entries[0].Kind != session.EntryPrompt || entries[2].Kind != session.EntryReply {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestAntigravityWithoutAConversationHasNothing(t *testing.T) {
	tr := AntigravityTranscript(t.TempDir(), "/c/none", "")
	if tr.ID != "" || tr.Dir != "" {
		t.Fatalf("transcript = %+v", tr)
	}
	if r, p := LastExchange(tr); r != nil || p != nil {
		t.Fatalf("exchange = %+v, %+v", r, p)
	}
	if q := PendingQuestion(tr); q != nil {
		t.Fatalf("question = %+v", q)
	}
}

func TestClearingAnAntigravitySessionWaitsForItsNewConversation(t *testing.T) {
	app := t.TempDir()
	tr := writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt}, nil)
	sent := make(chan struct{})
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name == "aoe" && strings.Contains(strings.Join(args, " "), "/clear") {
			close(sent)
		}
		return nil, nil
	}
	go func() {
		<-sent
		setLatest(t, app, "/c/knip", "conv-2")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", tr, time.Millisecond)
	if err != nil || id != "conv-2" {
		t.Fatalf("Clear = %q, %v; want conv-2", id, err)
	}
}

func TestClearingAnAntigravitySessionFindsItsNewBrainDirBeforeLastConversationsUpdates(t *testing.T) {
	app := t.TempDir()
	tr := writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt}, nil)
	if err := os.MkdirAll(filepath.Join(app, "brain", "older"), 0o755); err != nil {
		t.Fatal(err)
	}
	sent := make(chan struct{})
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name == "aoe" && strings.Contains(strings.Join(args, " "), "/clear") {
			close(sent)
		}
		return nil, nil
	}
	go func() {
		<-sent
		_ = os.MkdirAll(filepath.Join(app, "brain", "conv-2", "scratch"), 0o755)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", tr, time.Millisecond)
	if err != nil || id != "conv-2" {
		t.Fatalf("Clear = %q, %v; want conv-2", id, err)
	}

	// An empty new brain directory wins over another conversation that already has a transcript.
	before := antigravityConversations(app)
	active := filepath.Join(app, "brain", "subagent", ".system_generated", "logs")
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "transcript.jsonl"), []byte(agyPrompt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "brain", "conv-3", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := newAntigravityConversation(app, before, "conv-1"); got != "conv-3" {
		t.Fatalf("newAntigravityConversation = %q, want conv-3", got)
	}
}

func TestAoeListsAntigravitySessions(t *testing.T) {
	run := func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
		if args[2] == "list" {
			return []byte(`[{"id":"a","title":"knip","tool":"antigravity","state":"live","path":"/c/knip"},
				{"id":"b","title":"shell","tool":"antigravity","command":"/bin/zsh","state":"live","path":"/c/x"}]`), nil
		}
		return []byte(`{"id":"a","title":"knip","tool":"antigravity","state":"live","path":"/c/knip","status":"idle"}`), nil
	}
	got, err := (Aoe{Profile: "p", Run: run}).Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("sessions = %+v, want only the one running agy", got)
	}
}

func TestAntigravityModelsReadsEachIdAndName(t *testing.T) {
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name != "/bin/agy" || strings.Join(args, " ") != "models" {
			t.Fatalf("ran %s %v", name, args)
		}
		return []byte("Fetching available models...\ngemini-4-neon-high\tGemini 4 Neon (High)\nclaude-opus-5-5-high\tClaude Opus 5.5 (High)\n"), nil
	}
	models, err := AntigravityModels(context.Background(), run, "/bin/agy")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != (session.Model{ID: "gemini-4-neon-high", Name: "Gemini 4 Neon (High)"}) {
		t.Fatalf("models = %+v", models)
	}
	signedOut := func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte("Error: Please sign in to view available models."), nil
	}
	if _, err := AntigravityModels(context.Background(), signedOut, "/bin/agy"); err == nil {
		t.Fatal("a signed-out CLI listed models")
	}
}

func TestAnAntigravitySessionStatusIsDerivedFromItsTranscriptAndPaneThoughAoeSaysWaiting(t *testing.T) {
	app := t.TempDir()
	notGit := func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	var paneText string
	tmux := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name == "tmux" && len(args) > 0 && args[0] == "list-sessions" {
			return []byte("aoe_knip_12345678\n"), nil
		}
		if name == "tmux" && len(args) > 0 && args[0] == "capture-pane" {
			return []byte(paneText), nil
		}
		return nil, errors.New("unexpected call")
	}
	c := Collector{Aoe: Aoe{Run: tmux}, Run: notGit, AntigravityDir: app, Now: time.Now}
	agy := AoeSession{ID: "12345678abcd", Path: "/c/knip", Tool: session.Antigravity, Status: "waiting"}
	agyAsk := `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "tool_calls": [{"name": "ask_question", "args": {}}]}`
	runningPane := "⣿ Running command... 14s\n└ Tip: Use /skills\n────────\n>\n────────\nesc to cancel   auto · Gemini 4 Neon · high\n"
	approvalPane := "Allow running command?\n1. Allow once\n2. Deny\n"
	tests := []struct {
		name  string
		lines []string
		pane  string
		want  session.State
	}{
		{"a finished reply", []string{agyPrompt, agyTool, agyResult, agyReply}, "", session.Idle},
		{"a prompt being answered", []string{agyPrompt}, "", session.Working},
		{"a tool result back to the model", []string{agyPrompt, agyTool, agyResult}, "", session.Working},
		{"a tool call running in the pane", []string{agyPrompt, agyTool}, runningPane, session.Working},
		{"a tool call awaiting approval", []string{agyPrompt, agyTool}, approvalPane, session.NeedsInput},
		{"an ask_question tool call", []string{agyPrompt, agyAsk}, runningPane, session.NeedsInput},
	}
	for _, tt := range tests {
		paneText = tt.pane
		writeConversation(t, app, "/c/knip", "conv-1", tt.lines, []string{agyFull})
		if s := c.Snapshot(context.Background(), agy); s.State != tt.want {
			t.Errorf("%s: state = %v, want %v", tt.name, s.State, tt.want)
		}
	}
	// An empty conversation with no transcript yet is idle, not asking for input.
	setLatest(t, app, "/c/empty", "conv-empty")
	empty := AoeSession{ID: "12345678abcd", Path: "/c/empty", Tool: session.Antigravity, Status: "waiting"}
	if s := c.Snapshot(context.Background(), empty); s.State != session.Idle {
		t.Errorf("empty conversation: state = %v, want %v", s.State, session.Idle)
	}
}
