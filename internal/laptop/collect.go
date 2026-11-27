package laptop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Collector builds session snapshots from aoe, git and transcripts.
type Collector struct {
	Aoe Aoe
	Run Runner
	// ClaudeDirs are the Claude Code config directories, the agent's own
	// first. A project's environment can run Claude Code under another
	// profile than the agent, which keeps that project's transcripts there.
	ClaudeDirs []string
	// AoeHooks is where aoe's own Claude Code hook writes each session's
	// status, one directory per session id. aoe installs that hook in every
	// profile it starts, so it is the event for a session whose profile does
	// not run `peyk ping`.
	AoeHooks string
	// AntigravityDir is where Antigravity CLI keeps its conversations.
	AntigravityDir string
	// Root holds project repositories that can get a new session.
	Root string
	Now  func() time.Time
	// Prototypes, when set, finds the pages each session published.
	Prototypes *Prototypes
	// Logos, when set, finds each project's own mark.
	Logos *Logos
}

// Logo returns a project directory's mark as a data URI, or "".
func (c Collector) Logo(dir string) string {
	if c.Logos == nil {
		return ""
	}
	return c.Logos.Find(dir)
}

// Snapshot describes one aoe session.
func (c Collector) Snapshot(ctx context.Context, s AoeSession) session.Snapshot {
	git := RepoState(ctx, c.Run, s.Path)
	t := FindTranscript(c.ClaudeDirs, s.Path, s.AgentSessionID)
	if s.runsAntigravity() {
		t = AntigravityTranscript(c.AntigravityDir, s.Path, s.AgentSessionID)
	}
	snap := session.Snapshot{
		ID:         s.ID,
		Title:      s.Title,
		Path:       s.Path,
		AoeStatus:  s.Status,
		Tool:       t.Tool,
		State:      session.Derive(s.Status, Backgrounded(t), git),
		Git:        git,
		Transcript: t,
		UpdatedAt:  c.Now(),
	}
	// aoe's own status hook is Claude Code's; Antigravity's hooks are global.
	if c.AoeHooks != "" && t.Dir != "" && t.Tool == "" && !PingsPeyk(profileOf(t.Dir)) {
		snap.AoeHooks = filepath.Join(c.AoeHooks, s.ID)
	}
	c.Transcribe(&snap)
	return snap
}

// Transcribe fills in what a snapshot reads from its transcript.
func (c Collector) Transcribe(s *session.Snapshot) {
	s.SetExchange(LastExchange(s.Transcript))
	s.Question = nil
	if s.State == session.NeedsInput {
		s.Question = PendingQuestion(s.Transcript)
	}
	s.Prototypes = nil
	if c.Prototypes != nil {
		s.Prototypes = c.Prototypes.Find(s.Transcript)
	}
}

// Exchange rereads a session's latest reply and prompt from its transcript.
func (Collector) Exchange(t session.Transcript) (reply, prompt *session.Message) {
	return LastExchange(t)
}

// All describes every live Claude Code session in board order.
func (c Collector) All(ctx context.Context) ([]session.Snapshot, error) {
	sessions, err := c.Aoe.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]session.Snapshot, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, c.Snapshot(ctx, s))
	}
	slices.SortStableFunc(out, session.Compare)
	return out, nil
}

// Repos lists the git repositories directly under Root.
func (c Collector) Repos() ([]string, error) {
	if c.Root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		return nil, fmt.Errorf("list projects root: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(c.Root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			out = append(out, dir)
		}
	}
	return out, nil
}
