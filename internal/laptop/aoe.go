package laptop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AoeSession is one aoe session as `aoe list` and `aoe session show` report it.
type AoeSession struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Path           string `json:"path"`
	Tool           string `json:"tool"`
	Command        string `json:"command"`
	State          string `json:"state"`
	Status         string `json:"status"`
	AgentSessionID string `json:"agent_session_id"`
}

// Aoe talks to one aoe profile through its CLI.
type Aoe struct {
	Profile string
	Run     Runner
}

func (a Aoe) cmd(ctx context.Context, args ...string) ([]byte, error) {
	return a.Run(ctx, "", "aoe", append([]string{"-p", a.Profile}, args...)...)
}

// Sessions returns the live Claude Code sessions with their current status.
// Sessions running other tools, such as the shell that runs peyk itself, are
// left out: they have no transcript and no task to report.
func (a Aoe) Sessions(ctx context.Context) ([]AoeSession, error) {
	out, err := a.cmd(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	var all []AoeSession
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("decode aoe list: %w", err)
	}
	var live []AoeSession
	for _, s := range all {
		if s.State != "live" || !s.runsClaude() {
			continue
		}
		shown, err := a.Show(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		live = append(live, shown)
	}
	return live, nil
}

// runsClaude reports whether the session runs Claude Code. aoe can report a
// custom-command session's tool as claude after recovering it, so a command
// that does not start claude rules it out.
func (s AoeSession) runsClaude() bool {
	if s.Tool != "claude" {
		return false
	}
	fields := strings.Fields(s.Command)
	return len(fields) == 0 || filepath.Base(fields[0]) == "claude"
}

// Show returns one session including its status and agent session id.
func (a Aoe) Show(ctx context.Context, id string) (AoeSession, error) {
	out, err := a.cmd(ctx, "session", "show", id, "--json")
	if err != nil {
		return AoeSession{}, err
	}
	var s AoeSession
	if err := json.Unmarshal(out, &s); err != nil {
		return AoeSession{}, fmt.Errorf("decode aoe session %s: %w", id, err)
	}
	return s, nil
}

// Send delivers a prompt to a session, starting it if it is stopped.
func (a Aoe) Send(ctx context.Context, id, text string) error {
	_, err := a.cmd(ctx, "send", id, text)
	return err
}

// Clear sends /clear and waits, checking every poll, until Claude Code opens
// the new conversation for it in dir, the session's transcript directory, and
// returns that conversation's transcript id. Text sent before then could
// reach the old conversation or be typed into the clear itself. aoe's agent
// session id is not waited on: aoe records the new id anywhere from seconds
// to minutes later.
func (a Aoe) Clear(ctx context.Context, id, dir string, poll time.Duration) (string, error) {
	// Transcript timestamps have millisecond precision.
	since := time.Now().Truncate(time.Millisecond)
	if err := a.Send(ctx, id, "/clear"); err != nil {
		return "", err
	}
	for {
		if t := ClearedSince(dir, since); t != "" {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("no new conversation after /clear: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// Stop stops a session's agent process.
func (a Aoe) Stop(ctx context.Context, id string) error {
	_, err := a.cmd(ctx, "session", "stop", id)
	return err
}

// Create adds a stopped session for a directory and returns its id; Send
// starts it.
func (a Aoe) Create(ctx context.Context, dir, title, group string) (string, error) {
	if _, err := a.cmd(ctx, "add", dir, "-t", title, "-g", group); err != nil {
		return "", err
	}
	s, err := a.Show(ctx, title)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}
