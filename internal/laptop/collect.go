package laptop

import (
	"context"
	"sort"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Collector builds session snapshots from aoe, git and transcripts.
type Collector struct {
	Aoe       Aoe
	Run       Runner
	ClaudeDir string
	Now       func() time.Time
}

// Snapshot describes one aoe session.
func (c Collector) Snapshot(ctx context.Context, s AoeSession) session.Snapshot {
	git := RepoState(ctx, c.Run, s.Path)
	return session.Snapshot{
		ID:        s.ID,
		Title:     s.Title,
		Path:      s.Path,
		AoeStatus: s.Status,
		State:     session.Derive(s.Status, git),
		Git:       git,
		Message:   LastMessage(c.ClaudeDir, s.Path, s.AgentSessionID),
		UpdatedAt: c.Now(),
	}
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
