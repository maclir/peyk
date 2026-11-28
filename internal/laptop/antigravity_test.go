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
	reply, prompt, model := LastExchange(tr)
	if prompt == nil || prompt.Text != "Should we move to 3.8 Flash?" || prompt.ID != "0" {
		t.Fatalf("prompt = %+v, want only what was typed", prompt)
	}
	if reply == nil || reply.Text != "### Short answer\n\nYes, for four of five projects." || reply.ID != "3" {
		t.Fatalf("reply = %+v, want the whole reply from transcript_full.jsonl", reply)
	}
	if model != "Gemini 3.8 Flash (High)" || reply.Model != "Gemini 3.8 Flash (High)" || !reply.At.Equal(time.Date(2026, 9, 10, 9, 57, 0, 0, time.UTC)) {
		t.Fatalf("reply = %+v, model = %q", reply, model)
	}

	// Before the first reply lands, the prompt already names the model.
	working := writeConversation(t, app, "/c/knip", "conv-working", []string{agyPrompt, agyTool}, nil)
	r, p, m := LastExchange(working)
	if r != nil || p == nil || m != "Gemini 3.8 Flash (High)" {
		t.Fatalf("before first reply: reply = %+v, prompt = %+v, model = %q", r, p, m)
	}

	// A planner response's own model field still names the model after the
	// opening prompt is no longer in the tail.
	plannerOnly := writeConversation(t, app, "/c/knip", "conv-tail", []string{
		`{"step_index": 50, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "model": "gemini-4-neon-high", "content": "Done."}`,
	}, nil)
	if _, _, m := LastExchange(plannerOnly); m != "gemini-4-neon-high" {
		t.Fatalf("planner model = %q, want gemini-4-neon-high", m)
	}
}

func TestAntigravityBackgroundedTracksRunningTasks(t *testing.T) {
	app := t.TempDir()
	startCall := `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"make test\""}}]}`
	started := `{"step_index": 2, "source": "MODEL", "type": "GENERIC", "status": "RUNNING", "content": "Tool is running as a background task with task id: conv-1/task-2\nLog output:"}`
	waitingReply := `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "content": "Waiting for tests."}`
	finished := `{"step_index": 4, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "content": "[Message] content=Task id \"conv-1/task-2\" finished with result: ok"}`
	killed := `{"step_index": 4, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "tool_calls": [{"name": "manage_task", "args": {"Action": "\"kill\"", "TaskId": "\"conv-1/task-2\""}}]}`
	daemonCall := `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"npm run dev\"", "IsDaemon": "true"}}]}`

	tr := writeConversation(t, app, "/c/peyk", "conv-1", []string{startCall, started, waitingReply}, nil)
	if !Backgrounded(tr) {
		t.Fatal("running background task should report backgrounded")
	}
	tr = writeConversation(t, app, "/c/peyk", "conv-1", []string{startCall, started, waitingReply, finished}, nil)
	if Backgrounded(tr) {
		t.Fatal("finished background task should not report backgrounded")
	}
	tr = writeConversation(t, app, "/c/peyk", "conv-1", []string{startCall, started, waitingReply, killed}, nil)
	if Backgrounded(tr) {
		t.Fatal("killed background task should not report backgrounded")
	}
	tr = writeConversation(t, app, "/c/peyk", "conv-1", []string{daemonCall, started, waitingReply}, nil)
	if Backgrounded(tr) {
		t.Fatal("daemon background task should not report backgrounded")
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
	if r, p, m := LastExchange(tr); r != nil || p != nil || m != "" {
		t.Fatalf("exchange = %+v, %+v, %q", r, p, m)
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
	// A finished reply is idle even while aoe's tmux output window still reports running.
	writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt, agyTool, agyResult, agyReply}, []string{agyFull})
	stillRunning := AoeSession{ID: "12345678abcd", Path: "/c/knip", Tool: session.Antigravity, Status: "running"}
	if s := c.Snapshot(context.Background(), stillRunning); s.State != session.Idle {
		t.Errorf("finished reply while aoe reports running: state = %v, want %v", s.State, session.Idle)
	}
	// An empty conversation with no transcript yet is idle, not asking for input.
	setLatest(t, app, "/c/empty", "conv-empty")
	empty := AoeSession{ID: "12345678abcd", Path: "/c/empty", Tool: session.Antigravity, Status: "waiting"}
	if s := c.Snapshot(context.Background(), empty); s.State != session.Idle {
		t.Errorf("empty conversation: state = %v, want %v", s.State, session.Idle)
	}
}

func TestAntigravityProgressReadsStepsThinkingAndToolInCurrentTurn(t *testing.T) {
	app := t.TempDir()
	prompt2 := `{"step_index": 4, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-09-10T10:00:00Z", "content": "<USER_REQUEST>\nFix restarts\n</USER_REQUEST>"}`

	// A fresh prompt with no model steps yet must not inherit the prior turn's progress.
	tr := writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt, agyTool, agyResult, agyReply, prompt2}, nil)
	if p := Progress(tr); p != nil {
		t.Fatalf("after new prompt: progress = %+v, want nil", p)
	}

	// Thinking (first paragraph only) and file Path trimmed relative to workdir.
	readStep := `{"step_index": 5, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T10:00:05Z", "thinking": "Inspecting restart paths.\n\nSecond paragraph detail.", "tool_calls": [{"name": "read_file", "args": {"Path": "\"/c/knip/cmd/launchd.go\""}}]}`
	tr = writeConversation(t, app, "/c/knip", "conv-1", []string{agyPrompt, agyTool, agyResult, agyReply, prompt2, readStep}, nil)
	p := Progress(tr)
	if p == nil || p.Tool != "read_file" || p.Detail != "cmd/launchd.go" || p.Thinking != "Inspecting restart paths." {
		t.Fatalf("read step progress = %+v", p)
	}

	// Truncated tool_calls with update_steps falls back to transcript_full.jsonl for the checklist.
	cutSteps := `{"step_index": 6, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T10:00:15Z", "thinking": "**Editing**", "truncated_fields": ["tool_calls"], "tool_calls": [{"name": "update_steps", "args": {"steps": "[{\"status\":\"done\",\"title\":\"Inspect"}}, {"name": "edit_file", "args": {"Path": "\"/c/knip/main.go\""}}]}`
	fullSteps := `{"step_index": 6, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T10:00:15Z", "thinking": "**Editing**", "tool_calls": [{"name": "update_steps", "args": {"steps": "[{\"status\":\"done\",\"title\":\"Inspect restart paths\"},{\"status\":\"in_progress\",\"title\":\"Update launcher\"}]"}}, {"name": "edit_file", "args": {"Path": "\"/c/knip/main.go\""}}]}`
	tr = writeConversation(t, app, "/c/knip", "conv-1", []string{prompt2, readStep, cutSteps}, []string{fullSteps})
	p = Progress(tr)
	if p == nil || p.Tool != "edit_file" || p.Detail != "main.go" || p.Thinking != "Editing" {
		t.Fatalf("steps progress = %+v", p)
	}
	if len(p.Steps) != 2 || p.Steps[0] != (session.Step{Title: "Inspect restart paths", Status: "done"}) || p.Steps[1] != (session.Step{Title: "Update launcher", Status: "in_progress"}) {
		t.Fatalf("steps = %+v", p.Steps)
	}

	// A mid-turn visible reply suppresses its own and older thinking while keeping steps and tool.
	midReply := `{"step_index": 7, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T10:00:30Z", "content": "Updated launcher; running tests.", "thinking": "Running full test suite now.", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"make test\"", "toolSummary": "\"Run full test suite\""}}]}`
	tr = writeConversation(t, app, "/c/knip", "conv-1", []string{prompt2, readStep, cutSteps, midReply}, []string{fullSteps})
	p = Progress(tr)
	if p == nil || p.Tool != "run_command" || p.Detail != "Run full test suite" || p.Thinking != "" || len(p.Steps) != 2 {
		t.Fatalf("after mid-turn reply: progress = %+v", p)
	}

	// A newer tool-only step after the mid-turn reply surfaces its new thinking.
	afterMid := `{"step_index": 8, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-10T10:00:45Z", "thinking": "Tests passed, checking git status.", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "\"git status\"", "toolSummary": "\"Check git status\""}}]}`
	tr = writeConversation(t, app, "/c/knip", "conv-1", []string{prompt2, readStep, cutSteps, midReply, afterMid}, []string{fullSteps})
	p = Progress(tr)
	if p == nil || p.Tool != "run_command" || p.Detail != "Check git status" || p.Thinking != "Tests passed, checking git status." || len(p.Steps) != 2 {
		t.Fatalf("after subsequent step: progress = %+v", p)
	}
}
