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
	if g.Last == nil || g.Last.Subject != "Third" || len(g.Last.Hash) < 7 || g.Last.At.IsZero() {
		t.Fatalf("last commit: %+v", g.Last)
	}

	if err := os.Remove(filepath.Join(dir, "d.txt")); err != nil {
		t.Fatal(err)
	}
	if err := Push(ctx, Exec, dir); err != nil {
		t.Fatal(err)
	}
	if g = RepoState(ctx, Exec, dir); g.Unpushed != 0 {
		t.Fatalf("after Push: %+v", g)
	}
}

func TestRepoStateWithoutUpstreamCountsCommitsNoRemoteHas(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "a.txt", "Only")
	g := RepoState(context.Background(), Exec, dir)
	if g.Upstream != "" || g.Unpushed != 1 {
		t.Fatalf("want 1 unpushed and no upstream, got %+v", g)
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

func TestLastMessagePrefersLatestReplyAcrossTranscripts(t *testing.T) {
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

	m := LastMessage(claude, work, "old")
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

func TestLastMessageWithoutTranscripts(t *testing.T) {
	if m := LastMessage(t.TempDir(), "/nowhere", "x"); m != nil {
		t.Fatalf("want nil, got %+v", m)
	}
}

func TestLastMessageReadsOnlyTheTailOfLargeTranscripts(t *testing.T) {
	claude := t.TempDir()
	work := "/w"
	filler := `{"type":"user","text":"` + strings.Repeat("x", 1<<20) + `"}`
	lines := []string{assistant("2026-11-14T07:00:00Z", "too early")}
	for i := 0; i < 5; i++ {
		lines = append(lines, filler)
	}
	lines = append(lines, assistant("2026-11-14T10:00:00Z", "at the end"))
	writeTranscript(t, claude, work, "big", time.Now(), lines...)
	if m := LastMessage(claude, work, ""); m == nil || m.Text != "at the end" {
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
