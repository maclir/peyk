package laptop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Collector builds session snapshots from aoe, git and transcripts.
type Collector struct {
	Aoe       Aoe
	Run       Runner
	ClaudeDir string
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
	t := session.Transcript{Dir: TranscriptDir(c.ClaudeDir, s.Path), ID: s.AgentSessionID}
	snap := session.Snapshot{
		ID:         s.ID,
		Title:      s.Title,
		Path:       s.Path,
		AoeStatus:  s.Status,
		State:      session.Derive(s.Status, git),
		Git:        git,
		Transcript: t,
		UpdatedAt:  c.Now(),
	}
	snap.SetExchange(LastExchange(t))
	if c.Prototypes != nil {
		snap.Prototypes = c.Prototypes.Find(t)
	}
	return snap
}

// Exchange rereads a session's latest reply and prompt from its transcript.
func (Collector) Exchange(t session.Transcript) (reply, prompt *session.Message) {
	return LastExchange(t)
}

// All describes every live Claude Code session, the ones needing attention first.
func (c Collector) All(ctx context.Context) ([]session.Snapshot, error) {
	sessions, err := c.Aoe.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]session.Snapshot, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, c.Snapshot(ctx, s))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State.Rank() != out[j].State.Rank() {
			return out[i].State.Rank() < out[j].State.Rank()
		}
		return out[i].Title < out[j].Title
	})
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
