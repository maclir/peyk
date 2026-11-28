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
		"git@github.com:maclir/peyk.git":       "maclir/peyk",
		"https://github.com/maclir/relay.git":  "maclir/relay",
		"https://github.com/maclir/relay":      "maclir/relay",
		"ssh://git@github.com/maclir/peyk.git": "maclir/peyk",
		"git@gitlab.com:unbounds/dekode/x.git": "",
		"git@ghe.example.com:me/peyk.git":      "",
		"https://github.com/maclir":            "",
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

func TestFindTranscriptSearchesEveryProfile(t *testing.T) {
	own, other := t.TempDir(), t.TempDir()
	work := "/Users/me/code/other"
	old := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	writeTranscript(t, own, work, "a", old, "")
	writeTranscript(t, other, work, "b", old.Add(time.Hour), "")
	dirs := []string{own, other}

	if got := FindTranscript(dirs, work, "a"); got.Dir != TranscriptDir(own, work) {
		t.Fatalf("own id: got %s", got.Dir)
	}
	if got := FindTranscript(dirs, work, "b"); got.Dir != TranscriptDir(other, work) || got.ID != "b" {
		t.Fatalf("other profile's id: got %+v", got)
	}
	if got := FindTranscript(dirs, work, ""); got.Dir != TranscriptDir(other, work) {
		t.Fatalf("no id: got %s, want the newest transcript's profile", got.Dir)
	}
	if got := FindTranscript(dirs, "/elsewhere", "x"); got.Dir != TranscriptDir(own, "/elsewhere") {
		t.Fatalf("no transcript: got %s, want the agent's own profile", got.Dir)
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
	r, _, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: id})
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

func TestLastExchangeFindsAPromptSentWhileWorking(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "s", time.Now(),
		human("2026-11-14T09:00:00Z", `"Fix the bug"`),
		queued("2026-11-14T09:00:30Z", "Also add a test"),
		assistant("2026-11-14T09:01:00Z", "Done."),
	)
	_, p, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, "/w"), ID: "s"})
	if p == nil || p.Text != "Also add a test" {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestLastExchangeFindsAShellCommandAPersonSent(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "s", time.Now(),
		human("2026-11-14T09:00:00Z", `"continue"`),
		assistant("2026-11-14T09:00:05Z", "Done."),
		`{"type":"user","uuid":"b1","timestamp":"2026-11-14T09:01:00Z","message":{"role":"user","content":"<bash-input>git pushwhat is 3p?</bash-input>"}}`,
		`{"type":"user","timestamp":"2026-11-14T09:01:01Z","message":{"role":"user","content":[{"type":"text","text":"<bash-input>echo quoted</bash-input>"}]}}`,
		assistant("2026-11-14T09:01:05Z", "3p is a pool."),
	)
	_, p, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, "/w"), ID: "s"})
	if p == nil || p.ID != "b1" || p.Text != "! git pushwhat is 3p?" {
		t.Fatalf("prompt = %+v", p)
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
	r, p, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"})
	if r == nil || r.Text != "Both added." {
		t.Fatalf("reply = %+v", r)
	}
	if p == nil || p.Text != "Also --csv" || !p.At.Equal(time.Date(2026, 11, 14, 9, 1, 0, 0, time.UTC)) {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestExchangeNamesItsTranscriptEntries(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	writeTranscript(t, claude, work, "s", time.Now(),
		`{"type":"user","uuid":"p1","timestamp":"2026-11-14T09:00:00Z","origin":{"kind":"human"},"message":{"content":"Add a --json flag"}}`,
		`{"type":"assistant","uuid":"r1","timestamp":"2026-11-14T09:00:05Z","message":{"content":[{"type":"text","text":"Done."}]}}`,
	)
	s := session.Snapshot{Transcript: session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"}}
	s.SetExchange(LastExchange(s.Transcript))
	if s.Prompt.ID != "p1" || s.Message.ID != "r1" || s.Latest != "r1" || s.Conversation != "s" {
		t.Fatalf("snapshot = %+v", s)
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
	r, p, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "fresh"})
	if r != nil || p == nil || p.Text != "/clear" || !p.At.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("reply = %+v, prompt = %+v", r, p)
	}
	if _, p, _ := LastExchange(session.Transcript{Dir: TranscriptDir(claude, work), ID: "after"}); p == nil || p.Text != "Next task" {
		t.Fatalf("a prompt sent after the clear wins: %+v", p)
	}
}

func TestLastExchangeWithoutTranscripts(t *testing.T) {
	if r, p, m := LastExchange(session.Transcript{Dir: "/nowhere", ID: "x"}); r != nil || p != nil || m != "" {
		t.Fatalf("want nil, got %+v %+v %q", r, p, m)
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
	  {"id":"c","title":"relayServer","tool":"peyk","state":"live","path":"/r"},
	  {"id":"d","title":"peykServer","tool":"claude","command":"zsh -lc 'make serve'","state":"live","path":"/k"},
	  {"id":"e","title":"pinned","tool":"claude","command":"/opt/bin/claude --model x","state":"live","path":"/p"},
	  {"id":"f","title":"`+KeptTitle("f0f0f0f0f0f0", "knip")+`","tool":"claude","state":"live","path":"/k"}
	]`, map[string]string{
		"a": `{"id":"a","title":"knip","tool":"claude","state":"live","path":"/k","status":"waiting","agent_session_id":"s1"}`,
		"e": `{"id":"e","title":"pinned","tool":"claude","state":"live","path":"/p"}`,
	})
	got, err := Aoe{Profile: "personal", Run: run}.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "e" || got[0].ID != "a" || got[0].Status != "waiting" || got[0].AgentSessionID != "s1" {
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
	c := Collector{Aoe: Aoe{Profile: "personal", Run: notGit}, Run: notGit, ClaudeDirs: []string{t.TempDir()}, Now: func() time.Time { return now }}
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
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", session.Transcript{Dir: dir}, time.Millisecond)
		done <- result{id, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("returned before a new conversation: %v", r.err)
	case <-time.After(20 * time.Millisecond):
	}
	at := time.Now().Add(time.Second)
	writeTranscript(t, claude, work, "new", at, clearEntry(at.UTC().Format(time.RFC3339Nano)))
	if r := <-done; r.err != nil || r.id != "new" {
		t.Fatalf("id = %q, err = %v", r.id, r.err)
	}
	if got := strings.Join(calls, "|"); got != "send -- k /clear" {
		t.Fatalf("calls = %s", got)
	}
}

func TestClearGivesUpWhenNoNewConversationStarts(t *testing.T) {
	claude, work := t.TempDir(), "/w"
	run := func(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (Aoe{Profile: "p", Run: run}).Clear(ctx, "k", session.Transcript{Dir: TranscriptDir(claude, work)}, time.Millisecond)
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
	if id := ClearedSince(dir, since); id != "" {
		t.Fatalf("no transcript was opened by a /clear since then, got %q", id)
	}
	writeTranscript(t, claude, work, "new", later, `{"type":"mode"}`, clearEntry("2026-11-14T10:00:00Z"))
	if id := ClearedSince(dir, since); id != "new" {
		t.Fatalf("id = %q, want the transcript opened by /clear", id)
	}
	if ClearedSince(filepath.Join(claude, "missing"), since) != "" {
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

func launched(result string) string {
	return `{"type":"user","isSidechain":false,"timestamp":"2026-11-17T14:00:00Z","message":{"content":[{"type":"tool_result","content":"started"}]},"toolUseResult":` + result + `}`
}

func ended(id string) string {
	return `{"type":"user","isSidechain":false,"timestamp":"2026-11-17T14:05:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>` + id + `</task-id>\n<status>completed</status>\n</task-notification>"}}`
}

func TestBackgroundedUntilEveryTaskEnds(t *testing.T) {
	shell := launched(`{"stdout":"","backgroundTaskId":"bo7d3d3ap"}`)
	agent := launched(`{"isAsync":true,"status":"async_launched","agentId":"a9f3"}`)
	monitor := launched(`{"taskId":"bi6y","timeoutMs":600000,"persistent":false}`)
	watcher := launched(`{"taskId":"bper","persistent":true}`)
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"shell still running", []string{shell, assistant("2026-11-17T14:01:00Z", "tests are running")}, true},
		{"shell ended", []string{shell, ended("bo7d3d3ap")}, false},
		{"async agent still running", []string{agent}, true},
		{"one of two ended", []string{shell, agent, ended("a9f3")}, true},
		{"monitor still running", []string{monitor}, true},
		{"persistent monitor is not work in flight", []string{watcher}, false},
		{"stopped by the agent", []string{shell, `{"type":"assistant","isSidechain":false,"message":{"content":[{"type":"tool_use","id":"toolu_1","name":"TaskStop","input":{"task_id":"bo7d3d3ap"}}]}}`}, false},
		{"no tasks", []string{assistant("2026-11-17T14:01:00Z", "done")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude := t.TempDir()
			work := "/Users/me/code/areh"
			writeTranscript(t, claude, work, "s", time.Now(), tt.lines...)
			if got := Backgrounded(session.Transcript{Dir: TranscriptDir(claude, work), ID: "s"}); got != tt.want {
				t.Fatalf("Backgrounded = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackgroundedReadsOnlyTheSessionsOwnTranscript(t *testing.T) {
	claude := t.TempDir()
	work := "/Users/me/code/areh"
	writeTranscript(t, claude, work, "other", time.Now(), launched(`{"backgroundTaskId":"b1"}`))
	if Backgrounded(session.Transcript{Dir: TranscriptDir(claude, work)}) {
		t.Fatal("unrecorded transcript reported a background task")
	}
}

func TestResumePointsTheSessionAtTheConversationAndRelaunchesIt(t *testing.T) {
	claude, work := t.TempDir(), "/w"
	dir := TranscriptDir(claude, work)
	writeTranscript(t, claude, work, "older", time.Now(), clearEntry("2026-11-14T09:00:00Z"))
	for _, tt := range []struct {
		stopped bool
		want    string
	}{
		{false, "session set-session-id k older|session restart k"},
		{true, "session set-session-id k older|session start k"},
	} {
		var calls []string
		run := func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args[2:], " "))
			return nil, nil
		}
		if err := (Aoe{Profile: "p", Run: run}).Resume(context.Background(), "k", session.Transcript{Dir: dir}, "older", tt.stopped); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(calls, "|"); got != tt.want {
			t.Errorf("stopped=%v: calls = %s", tt.stopped, got)
		}
	}
}

func TestResumeRefusesAConversationFromAnotherProject(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/elsewhere", "other", time.Now(), clearEntry("2026-11-14T09:00:00Z"))
	run := func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("aoe was asked to resume a conversation this session cannot reach")
		return nil, nil
	}
	err := (Aoe{Profile: "p", Run: run}).Resume(context.Background(), "k", session.Transcript{Dir: TranscriptDir(claude, "/w")}, "other", false)
	if err == nil || !strings.Contains(err.Error(), "no conversation other") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendPassesAMessageStartingWithADashAsTheMessage(t *testing.T) {
	var args []string
	run := func(_ context.Context, _ string, _ string, a ...string) ([]byte, error) {
		args = a
		return nil, nil
	}
	if err := (Aoe{Profile: "p", Run: run}).Send(context.Background(), "k", "- fix it"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args[2:], "|"); got != "send|--|k|- fix it" {
		t.Fatalf("args = %s", got)
	}
}

func TestLastExchangeNamesTheModelThatWroteTheReply(t *testing.T) {
	claude := t.TempDir()
	writeTranscript(t, claude, "/w", "s", time.Now(),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"On Opus."}]}}`,
		`{"type":"assistant","timestamp":"2026-11-14T09:01:00Z","message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error"}]}}`,
	)
	if m := reply(claude, "/w", "s"); m == nil || m.Text != "API Error" || m.Model != "claude-opus-5-5" {
		t.Fatalf("a reply no model wrote names the model of the one before: %+v", m)
	}
	writeTranscript(t, claude, "/w", "s", time.Now(),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:00Z","message":{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"On Sonnet."}]}}`,
	)
	if m := reply(claude, "/w", "s"); m == nil || m.Model != "claude-sonnet-5-5" {
		t.Fatalf("reply = %+v, want it named for claude-sonnet-5-5", m)
	}
	// A tool call before any text reply still names the model.
	writeTranscript(t, claude, "/w", "tool", time.Now(),
		human("2026-11-14T09:00:00Z", `"Check files"`),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:02Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Read","input":{}}]}}`,
	)
	if r, _, model := LastExchange(session.Transcript{Dir: TranscriptDir(claude, "/w"), ID: "tool"}); r != nil || model != "claude-opus-5-5" {
		t.Fatalf("tool-only turn: reply = %+v, model = %q", r, model)
	}
}

func TestSnapshotDefaultModelComesFromLaunchFlagOrSettings(t *testing.T) {
	claude := t.TempDir()
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	notGit := func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	c := Collector{Run: notGit, ClaudeDirs: []string{claude}, Now: time.Now}
	fromSettings := c.Snapshot(context.Background(), AoeSession{ID: "a", Path: "/w", Tool: "claude", Status: "idle"})
	if fromSettings.DefaultModel != "opus" {
		t.Fatalf("from settings = %q, want opus", fromSettings.DefaultModel)
	}
	fromFlag := c.Snapshot(context.Background(), AoeSession{ID: "b", Path: "/w", Tool: "claude", Command: "claude --model sonnet", Status: "idle"})
	if fromFlag.DefaultModel != "sonnet" {
		t.Fatalf("from flag = %q, want sonnet", fromFlag.DefaultModel)
	}
	fromEq := c.Snapshot(context.Background(), AoeSession{ID: "c", Path: "/w", Tool: session.Antigravity, Command: "agy --model=gemini-4-neon-high", Status: "idle"})
	if fromEq.DefaultModel != "gemini-4-neon-high" {
		t.Fatalf("from --model= = %q, want gemini-4-neon-high", fromEq.DefaultModel)
	}
}

func TestRename(t *testing.T) {
	var calls []string
	a := Aoe{Profile: "personal", Run: func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args[2:], " "))
		return nil, nil
	}}
	if err := a.Rename(context.Background(), "7ab0c36356b64513", "peyk~7ab0c363"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, "|"); got != "session rename 7ab0c36356b64513 -t peyk~7ab0c363" {
		t.Fatalf("calls = %s", got)
	}
}

func TestClaudeProgressReadsLatestToolAndTodosInCurrentTurn(t *testing.T) {
	claude := t.TempDir()
	work := "/c/knip"
	writeTranscript(t, claude, work, "s", time.Now(),
		human("2026-11-14T09:00:00Z", `"First task"`),
		`{"type":"assistant","timestamp":"2026-11-14T09:00:05Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"old cmd"}}]}}`,
		human("2026-11-14T09:05:00Z", `"Second task"`),
	)
	tr := FindTranscript([]string{claude}, work, "s")
	if p := Progress(tr); p != nil {
		t.Fatalf("before any tool in current turn: progress = %+v, want nil", p)
	}

	writeTranscript(t, claude, work, "s", time.Now(),
		human("2026-11-14T09:05:00Z", `"Second task"`),
		`{"type":"assistant","timestamp":"2026-11-14T09:05:02Z","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"content":"Read code","status":"completed"},{"content":"Edit file","status":"in_progress"}]}}]}}`,
		`{"type":"assistant","timestamp":"2026-11-14T09:05:04Z","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/c/knip/internal/app.go"}}]}}`,
	)
	p := Progress(tr)
	if p == nil || p.Tool != "Edit" || p.Detail != "internal/app.go" {
		t.Fatalf("claude progress = %+v", p)
	}
	if len(p.Steps) != 2 || p.Steps[0] != (session.Step{Title: "Read code", Status: "done"}) || p.Steps[1] != (session.Step{Title: "Edit file", Status: "in_progress"}) {
		t.Fatalf("claude steps = %+v", p.Steps)
	}

	notGit := func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	c := Collector{Run: notGit, ClaudeDirs: []string{claude}, Now: time.Now}
	working := c.Snapshot(context.Background(), AoeSession{ID: "a", Path: work, AgentSessionID: "s", Status: "running"})
	if working.Progress == nil || working.Progress.Tool != "Edit" {
		t.Fatalf("working snapshot progress = %+v", working.Progress)
	}
	idle := c.Snapshot(context.Background(), AoeSession{ID: "a", Path: work, AgentSessionID: "s", Status: "idle"})
	if idle.Progress != nil {
		t.Fatalf("idle snapshot kept progress = %+v", idle.Progress)
	}
}
