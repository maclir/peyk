package laptop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		mustGit(t, dir, args...)
	}
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Exec(context.Background(), dir, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, file, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(subject), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", file)
	mustGit(t, dir, "commit", "-q", "-m", subject)
}

func TestRepoStateOutsideRepository(t *testing.T) {
	if g := RepoState(context.Background(), Exec, t.TempDir()); g != nil {
		t.Fatalf("want nil outside a repository, got %+v", g)
	}
}

func TestRepoStateCountsUnpushedAndDirty(t *testing.T) {
	ctx := context.Background()
	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := gitRepo(t)
	commit(t, dir, "a.txt", "First")
	mustGit(t, dir, "remote", "add", "origin", remote)
	mustGit(t, dir, "push", "-q", "-u", "origin", "main")

	g := RepoState(ctx, Exec, dir)
	if g.Branch != "main" || g.Upstream != "origin/main" || g.Unpushed != 0 || g.Dirty != 0 {
		t.Fatalf("after push: %+v", g)
	}

	commit(t, dir, "b.txt", "Second")
	commit(t, dir, "c.txt", "Third")
	if err := os.WriteFile(filepath.Join(dir, "d.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	g = RepoState(ctx, Exec, dir)
	if g.Unpushed != 2 || g.Dirty != 1 {
		t.Fatalf("want 2 unpushed and 1 dirty, got %+v", g)
	}
	if len(g.Pending) != 2 || g.Pending[0].Subject != "Third" || g.Pending[1].Subject != "Second" || g.Pending[0].Hash != g.Last.Hash {
		t.Fatalf("pending commits, newest first: %+v", g.Pending)
	}
	if g.Last == nil || g.Last.Subject != "Third" || len(g.Last.Hash) < 7 || g.Last.At.IsZero() {
		t.Fatalf("last commit: %+v", g.Last)
	}

	if err := os.Remove(filepath.Join(dir, "d.txt")); err != nil {
		t.Fatal(err)
	}
	if err := Push(ctx, Exec, dir); err != nil {
		t.Fatal(err)
	}
	if g = RepoState(ctx, Exec, dir); g.Unpushed != 0 || g.Pending != nil {
		t.Fatalf("after Push: %+v", g)
	}
}

func TestRepoStateWithoutUpstreamCountsCommitsNoRemoteHas(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "a.txt", "Only")
	g := RepoState(context.Background(), Exec, dir)
	if g.Upstream != "" || g.Unpushed != 1 || len(g.Pending) != 1 || g.Pending[0].Subject != "Only" {
		t.Fatalf("want 1 unpushed and no upstream, got %+v", g)
	}
}

func TestRepoStateNamesTheGitHubRepositoryOnlyWithWorkflows(t *testing.T) {
	ctx := context.Background()
	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := gitRepo(t)
	commit(t, dir, "a.txt", "First")
	mustGit(t, dir, "remote", "add", "origin", remote)
	mustGit(t, dir, "push", "-q", "-u", "origin", "main")
	mustGit(t, dir, "remote", "set-url", "origin", "git@github.com:maclir/knip.git")
	head := strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))

	if g := RepoState(ctx, Exec, dir); g.Pushed != head || g.Repo != "" {
		t.Fatalf("without workflows: pushed %q repo %q", g.Pushed, g.Repo)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if g := RepoState(ctx, Exec, filepath.Join(dir, ".github")); g.Repo != "maclir/knip" {
		t.Fatalf("with workflows, from a subdirectory: repo %q", g.Repo)
	}
}

func TestGitHubRepo(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:maclir/peyk.git":        "maclir/peyk",
		"https://github.com/maclir/relay.git":   "maclir/relay",
		"https://github.com/maclir/relay":       "maclir/relay",
		"ssh://git@github.com/maclir/peyk.git":  "maclir/peyk",
		"git@gitlab.com:unbounds/dekode/x.git":  "",
		"git@ghe.example.com:me/peyk.git": "",
		"https://github.com/maclir":             "",
	} {
		if got := GitHubRepo(url); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestPushSetsUpstreamOnFirstPush(t *testing.T) {
	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := gitRepo(t)
	commit(t, dir, "a.txt", "First")
	mustGit(t, dir, "remote", "add", "origin", remote)
	if err := Push(context.Background(), Exec, dir); err != nil {
		t.Fatal(err)
	}
	if g := RepoState(context.Background(), Exec, dir); g.Upstream != "origin/main" || g.Unpushed != 0 {
		t.Fatalf("after first push: %+v", g)
	}
}

func TestTranscriptDir(t *testing.T) {
	got := TranscriptDir("/c", "/Users/me/code/maclir.net")
	if want := "/c/projects/-Users-me-code-maclir-net"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func writeTranscript(t *testing.T, claudeDir, workdir, id string, mod time.Time, lines ...string) {
	t.Helper()
	dir := TranscriptDir(claudeDir, workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func assistant(ts string, texts ...string) string {
	var content []string
	for _, x := range texts {
		content = append(content, `{"type":"text","text":`+quote(x)+`}`)
	}
	content = append(content, `{"type":"tool_use","name":"Bash","input":{}}`)
	return `{"type":"assistant","isSidechain":false,"timestamp":"` + ts + `","message":{"content":[` + strings.Join(content, ",") + `]}}`
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func human(ts, content string) string {
	return `{"type":"user","isSidechain":false,"timestamp":"` + ts + `","origin":{"kind":"human"},"message":{"role":"user","content":` + content + `}}`
}

func reply(claude, work, id string) *session.Message {
	r, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: id})
	return r
}

func TestLastExchangeUsesTheSessionsOwnTranscript(t *testing.T) {
	claude := t.TempDir()
	work := "/Users/me/code/knip"
	now := time.Now()
	writeTranscript(t, claude, work, "old", now.Add(-time.Hour),
		`{"type":"user","timestamp":"2026-11-14T08:00:00Z"}`,
		assistant("2026-11-14T08:00:01Z", "Old reply"),
	)
	writeTranscript(t, claude, work, "current", now.Add(-time.Minute),
		assistant("2026-11-14T09:00:00Z", "First paragraph.", "Committed `3f2a1c4`.\n\n- item"),
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-14T09:05:00Z","message":{"content":[{"type":"text","text":"subagent"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-14T09:06:00Z","message":{"content":[{"type":"tool_use"}]}}`,
		`{"type":"cost-state"}`,
	)
	// Started by /clear: newest file, no reply yet.
	writeTranscript(t, claude, work, "cleared", now, `{"type":"system"}`)

	if m := reply(claude, work, "old"); m == nil || m.Text != "Old reply" {
		t.Fatalf("a session's own transcript wins even when another is newer: %+v", m)
	}
	if m := reply(claude, work, "cleared"); m != nil {
		t.Fatalf("a session just cleared has no reply yet: %+v", m)
	}
	m := reply(claude, work, "")
	if m == nil {
		t.Fatal("no message")
	}
	if want := "First paragraph.\n\nCommitted `3f2a1c4`.\n\n- item"; m.Text != want {
		t.Fatalf("text = %q, want %q", m.Text, want)
	}
	if !m.At.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("at = %v", m.At)
	}
}

func TestLastExchangeFindsThePromptAPersonSent(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	writeTranscript(t, claude, work, "s", time.Now(),
		human("2026-11-14T09:00:00Z", `"Add a --json flag"`),
		assistant("2026-11-14T09:00:05Z", "On it."),
		human("2026-11-14T09:01:00Z", `[{"type":"image"},{"type":"text","text":"Also --csv"}]`),
		`{"type":"user","timestamp":"2026-11-14T09:01:01Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-11-14T09:01:02Z","message":{"content":"<local-command-caveat>x</local-command-caveat>"}}`,
		`{"type":"user","timestamp":"2026-11-14T09:01:03Z","message":{"content":"<command-name>/clear</command-name>"}}`,
		assistant("2026-11-14T09:02:00Z", "Both added."),
	)
	r, p := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"})
	if r == nil || r.Text != "Both added." {
		t.Fatalf("reply = %+v", r)
	}
	if p == nil || p.Text != "Also --csv" || !p.At.Equal(time.Date(2026, 11, 14, 9, 1, 0, 0, time.UTC)) {
		t.Fatalf("prompt = %+v", p)
	}
}

func clearEntry(ts string) string {
	return `{"type":"user","isSidechain":false,"timestamp":"` + ts + `","message":{"role":"user","content":"<command-name>/clear</command-name>\n            <command-message>clear</command-message>"}}`
}

func TestLastExchangeReportsTheClearThatStartedTheConversation(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	writeTranscript(t, claude, work, "fresh", time.Now(), clearEntry("2026-11-14T09:00:00Z"))
	writeTranscript(t, claude, work, "after", time.Now(),
		clearEntry("2026-11-14T09:00:00Z"),
		human("2026-11-14T09:01:00Z", `"Next task"`),
	)
	r, p := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "fresh"})
	if r != nil || p == nil || p.Text != "/clear" || !p.At.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("reply = %+v, prompt = %+v", r, p)
	}
	if _, p := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "after"}); p == nil || p.Text != "Next task" {
		t.Fatalf("a prompt sent after the clear wins: %+v", p)
	}
}

func TestLastExchangeWithoutTranscripts(t *testing.T) {
	if r, p := LastExchange(session.Transcript{Dir: "/nowhere", ID: "x"}); r != nil || p != nil {
		t.Fatalf("want nil, got %+v %+v", r, p)
	}
}

func TestLastExchangeReadsOnlyTheTailOfLargeTranscripts(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	filler := `{"type":"user","text":"` + strings.Repeat("x", 1<<20) + `"}`
	lines := []string{assistant("2026-11-14T07:00:00Z", "too early")}
	for i := 0; i < 5; i++ {
		lines = append(lines, filler)
	}
	lines = append(lines, assistant("2026-11-14T10:00:00Z", "at the end"))
	writeTranscript(t, claude, work, "big", time.Now(), lines...)
	if m := reply(claude, work, ""); m == nil || m.Text != "at the end" {
		t.Fatalf("got %+v", m)
	}
}

// fakeAoe answers aoe CLI calls from canned JSON.
func fakeAoe(list string, shows map[string]string) Runner {
	return func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name != "aoe" || len(args) < 3 || args[0] != "-p" || args[1] != "personal" {
			return nil, errors.New("unexpected call")
		}
		switch {
		case args[2] == "list":
			return []byte(list), nil
		case args[2] == "session" && args[3] == "show":
			if out, ok := shows[args[4]]; ok {
				return []byte(out), nil
			}
		}
		return nil, errors.New("unexpected aoe call: " + strings.Join(args, " "))
	}
}

func TestSessionsKeepsLiveClaudeSessions(t *testing.T) {
	run := fakeAoe(`[
	  {"id":"a","title":"knip","tool":"claude","state":"live","path":"/k"},
	  {"id":"b","title":"old","tool":"claude","state":"trashed","path":"/o"},
	  {"id":"c","title":"relayServer","tool":"peyk","state":"live","path":"/r"}
	]`, map[string]string{
		"a": `{"id":"a","title":"knip","tool":"claude","state":"live","path":"/k","status":"waiting","agent_session_id":"s1"}`,
	})
	got, err := Aoe{Profile: "personal", Run: run}.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" || got[0].Status != "waiting" || got[0].AgentSessionID != "s1" {
		t.Fatalf("got %+v", got)
	}
}

func TestCollectorAllSortsByAttention(t *testing.T) {
	aoeRun := fakeAoe(`[
	  {"id":"a","title":"areh","tool":"claude","state":"live","path":"/a"},
	  {"id":"h","title":"hamta","tool":"claude","state":"live","path":"/h"}
	]`, map[string]string{
		"a": `{"id":"a","title":"areh","tool":"claude","state":"live","path":"/a","status":"idle"}`,
		"h": `{"id":"h","title":"hamta","tool":"claude","state":"live","path":"/h","status":"waiting"}`,
	})
	notGit := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name == "git" {
			return nil, errors.New("not a repository")
		}
		return aoeRun(ctx, dir, name, args...)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	c := Collector{Aoe: Aoe{Profile: "personal", Run: notGit}, Run: notGit, ClaudeDir: t.TempDir(), Now: func() time.Time { return now }}
	got, err := c.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "hamta" || got[0].State != session.NeedsInput || got[1].State != session.Idle {
		t.Fatalf("got %+v", got)
	}
	if !got[0].UpdatedAt.Equal(now) || got[0].Git != nil {
		t.Fatalf("got %+v", got[0])
	}
}

func TestReposListsGitRepositoriesUnderRoot(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/.git", "b", ".hidden/.git", "c/.git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Collector{Root: root}.Repos()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "c")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := (Collector{Root: filepath.Join(root, "missing")}).Repos(); err == nil {
		t.Fatal("a missing root should fail, not publish an empty picker")
	}
}

func TestClearWaitsForTheNewConversationInTheTranscripts(t *testing.T) {
	claude, work := t.TempDir(), "/w"
	dir := TranscriptDir(claude, work)
	writeTranscript(t, claude, work, "old", time.Now(), clearEntry("2026-11-14T09:00:00Z"))
	var calls []string
	run := func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args[2:], " "))
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", dir, time.Millisecond) }()
	select {
	case err := <-done:
		t.Fatalf("returned before a new conversation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	at := time.Now().Add(time.Second)
	writeTranscript(t, claude, work, "new", at, clearEntry(at.UTC().Format(time.RFC3339Nano)))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, "|"); got != "send k /clear" {
		t.Fatalf("calls = %s", got)
	}
}

func TestClearGivesUpWhenNoNewConversationStarts(t *testing.T) {
	claude, work := t.TempDir(), "/w"
	run := func(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", TranscriptDir(claude, work), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no new conversation") {
		t.Fatalf("err = %v", err)
	}
}

func TestClearedSinceNeedsATranscriptOpenedByALaterClear(t *testing.T) {
	claude, work := t.TempDir(), "/w"
	dir := TranscriptDir(claude, work)
	since := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	later := since.Add(time.Hour)
	writeTranscript(t, claude, work, "earlier", later, clearEntry("2026-11-14T09:59:59.999Z"))
	writeTranscript(t, claude, work, "fresh", later, `{"type":"mode"}`, human("2026-11-14T10:00:01Z", `"hi"`))
	writeTranscript(t, claude, work, "sidechain", later, `{"type":"user","isSidechain":true,"timestamp":"2026-11-14T10:00:01Z","message":{"content":"<command-name>/clear</command-name>"}}`)
	if ClearedSince(dir, since) {
		t.Fatal("no transcript was opened by a /clear since then")
	}
	writeTranscript(t, claude, work, "new", later, `{"type":"mode"}`, clearEntry("2026-11-14T10:00:00Z"))
	if !ClearedSince(dir, since) {
		t.Fatal("missed the transcript opened by /clear")
	}
	if ClearedSince(filepath.Join(claude, "missing"), since) {
		t.Fatal("a missing directory holds no new conversation")
	}
}

func TestSnapshotIsClearedOnlyWithAnEmptyOwnTranscript(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	writeTranscript(t, claude, work, "empty", time.Now(), `{"type":"system"}`)
	writeTranscript(t, claude, work, "used", time.Now(), human("2026-11-14T09:00:00Z", `"hi"`))
	writeTranscript(t, claude, work, "byclear", time.Now(), clearEntry("2026-11-14T09:00:00Z"))
	for _, tt := range []struct {
		id      string
		cleared bool
	}{{"empty", true}, {"used", false}, {"byclear", true}, {"", false}} {
		s := session.Snapshot{Transcript: session.Transcript{Dir: TranscriptDir(claude, work), ID: tt.id}}
		s.SetExchange(LastExchange(s.Transcript))
		if s.Cleared != tt.cleared {
			t.Errorf("transcript %q: cleared = %v", tt.id, s.Cleared)
		}
	}
}

func ask(ts, id string) string {
	return `{"type":"assistant","isSidechain":false,"timestamp":"` + ts + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"AskUserQuestion","input":{"questions":[` +
		`{"question":"Which colour?","header":"Colour","multiSelect":false,"options":[{"label":"Red","description":"warm"},{"label":"Blue","description":"cool"}]},` +
		`{"question":"Which pets?","header":"Pets","multiSelect":true,"options":[{"label":"Cat","description":""},{"label":"Dog","description":""}]}]}}]}}`
}

func result(id string) string {
	return `{"type":"user","isSidechain":false,"message":{"content":[{"type":"tool_result","content":"x","tool_use_id":"` + id + `"}]}}`
}

func TestPendingQuestionIsTheOpenDialog(t *testing.T) {
	claude := t.TempDir()
	work := "/Users/me/code/knip"
	writeTranscript(t, claude, work, "s", time.Now(),
		ask("2026-11-14T09:00:00Z", "toolu_old"),
		result("toolu_old"),
		`{"type":"api-request-shape","shape":{"tools":[{"name":"AskUserQuestion","description":"..."}]}}`,
		ask("2026-11-14T09:05:00Z", "toolu_new"),
	)
	tr := session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"}
	q := PendingQuestion(tr)
	if q == nil || q.ID != "toolu_new" || len(q.Items) != 2 {
		t.Fatalf("question = %+v", q)
	}
	if it := q.Items[1]; it.Header != "Pets" || !it.MultiSelect || len(it.Options) != 2 || it.Options[1].Label != "Dog" {
		t.Fatalf("second tab = %+v", it)
	}
	if q.Items[0].Options[0].Description != "warm" || !q.At.Equal(time.Date(2026, 11, 14, 9, 5, 0, 0, time.UTC)) {
		t.Fatalf("first tab = %+v at %v", q.Items[0], q.At)
	}
	if done, _ := Outcome(tr, "toolu_new"); done {
		t.Fatal("open dialog reported done")
	}
	if done, refused := Outcome(tr, "toolu_old"); !done || refused {
		t.Fatalf("answered dialog: done=%v refused=%v", done, refused)
	}
}

func TestPendingQuestionIsGoneOnceAnswered(t *testing.T) {
	claude := t.TempDir()
	work := "/Users/me/code/knip"
	writeTranscript(t, claude, work, "s", time.Now(),
		ask("2026-11-14T09:00:00Z", "toolu_1"),
		result("toolu_1"),
		assistant("2026-11-14T09:01:00Z", "Going ahead."),
	)
	if q := PendingQuestion(session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"}); q != nil {
		t.Fatalf("answered dialog still pending: %+v", q)
	}
	if q := PendingQuestion(session.Transcript{Dir: TranscriptDir(claude, work)}); q != nil {
		t.Fatalf("a session without its own transcript has no question: %+v", q)
	}
}
