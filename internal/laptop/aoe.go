package laptop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
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

// Sessions returns the live Claude Code and Antigravity sessions with their
// current status. Sessions running other tools, such as the shell that runs
// peyk itself, are left out: peyk cannot read their conversations.
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
		if s.State != "live" || isKept(s.Title) || !s.runsClaude() && !s.runsAntigravity() {
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

// runsAntigravity reports whether the session runs Antigravity CLI, by
// aoe's tool name and, when the session has its own command, that command.
func (s AoeSession) runsAntigravity() bool {
	if s.Tool != session.Antigravity {
		return false
	}
	fields := strings.Fields(s.Command)
	return len(fields) == 0 || filepath.Base(fields[0]) == "agy"
}

// commandModel returns the model named by --model in a session's launch
// command, or "" when the command names none.
func commandModel(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == "--model" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], `"'`)
		}
		if v, ok := strings.CutPrefix(f, "--model="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
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
	_, err := a.cmd(ctx, "send", "--", id, text)
	return err
}

// Deliver sends text to a session and, when t can record a human prompt for
// text, waits up to wait (or until ctx ends when wait <= 0) for the prompt to
// appear in t's transcript. Slash commands and sessions without a known
// transcript location return once aoe send succeeds.
func (a Aoe) Deliver(ctx context.Context, id string, t session.Transcript, text string, wait, poll time.Duration) error {
	verify := canVerifyPrompt(t, text)
	var before *session.Message
	if verify {
		before = latestPrompt(t)
	}
	if err := a.Send(ctx, id, text); err != nil {
		return err
	}
	if !verify {
		return nil
	}
	waitCtx := ctx
	if wait > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	for {
		if promptAdvanced(before, latestPrompt(t)) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("the session did not record the prompt: %w", waitCtx.Err())
		case <-time.After(poll):
		}
	}
}

// canVerifyPrompt reports whether sending text to t can be observed as a human
// prompt in t's transcript. Slash commands such as /model do not write a human
// prompt entry, and a newly created session has no transcript location yet.
func canVerifyPrompt(t session.Transcript, text string) bool {
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return false
	}
	if t.Tool == session.Antigravity {
		return t.Dir != "" || (t.AppDir != "" && t.Workdir != "")
	}
	return t.Dir != ""
}

func latestPrompt(t session.Transcript) *session.Message {
	if t.Tool == session.Antigravity && t.ID == "" {
		if id := LatestAntigravityConversation(t.AppDir, t.Workdir); id != "" {
			t = t.WithID(id)
		}
	}
	_, p, _ := LastExchange(t)
	return p
}

func promptAdvanced(before, after *session.Message) bool {
	if after == nil || after.Text == session.ClearCommand {
		return false
	}
	if before == nil {
		return true
	}
	return after.ID != before.ID || !after.At.Equal(before.At) || after.Text != before.Text
}

// Clear sends /clear and waits, checking every poll, until Claude Code opens
// the new conversation for it in dir, the session's transcript directory, and
// returns that conversation's transcript id. Text sent before then could
// reach the old conversation or be typed into the clear itself. aoe's agent
// session id is not waited on: aoe records the new id anywhere from seconds
// to minutes later.
func (a Aoe) Clear(ctx context.Context, id string, t session.Transcript, poll time.Duration) (string, error) {
	// Transcript timestamps have millisecond precision.
	since := time.Now().Truncate(time.Millisecond)
	var before map[string]bool
	if t.Tool == session.Antigravity {
		before = antigravityConversations(t.AppDir)
	}
	if err := a.Send(ctx, id, "/clear"); err != nil {
		return "", err
	}
	for {
		if c := clearedTo(t, since, before); c != "" {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("no new conversation after /clear: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// clearedTo is the conversation a /clear opened, or "" until there is one.
// Antigravity writes cache/last_conversations.json only on a conversation's
// first prompt, so /clear is also observed from the directory it creates
// under brain/.
func clearedTo(t session.Transcript, since time.Time, before map[string]bool) string {
	if t.Tool == session.Antigravity {
		if c := LatestAntigravityConversation(t.AppDir, t.Workdir); c != "" && c != t.ID {
			return c
		}
		return newAntigravityConversation(t.AppDir, before, t.ID)
	}
	return ClearedSince(t.Dir, since)
}

// Stop stops a session's agent process.
func (a Aoe) Stop(ctx context.Context, id string) error {
	_, err := a.cmd(ctx, "session", "stop", id)
	return err
}

// Start launches a stopped session's agent process and waits until it is ready
// for input.
func (a Aoe) Start(ctx context.Context, id string, poll time.Duration) error {
	if _, err := a.cmd(ctx, "session", "start", id); err != nil {
		return err
	}
	return a.waitReady(ctx, id, poll)
}

// Restart relaunches a session's agent process on its conversation and waits
// until it is ready for input.
func (a Aoe) Restart(ctx context.Context, id string, poll time.Duration) error {
	if _, err := a.cmd(ctx, "session", "restart", id); err != nil {
		return err
	}
	return a.waitReady(ctx, id, poll)
}

// Resume points a session at conversation, one of its working directory's,
// relaunches it there (or launches it if stopped), and waits until it is ready
// for input.
func (a Aoe) Resume(ctx context.Context, id string, t session.Transcript, conversation string, stopped bool, poll time.Duration) error {
	path := filepath.Join(t.Dir, conversation+".jsonl")
	if t.Tool == session.Antigravity {
		path = filepath.Join(t.AppDir, "brain", conversation)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no conversation %s for this session: %w", conversation, err)
	}
	if _, err := a.cmd(ctx, "session", "set-session-id", id, conversation); err != nil {
		return err
	}
	if stopped {
		return a.Start(ctx, id, poll)
	}
	return a.Restart(ctx, id, poll)
}

// waitReady polls aoe until the session reaches a prompt state: "idle", or
// "waiting" for Antigravity (where aoe reports an idle auto-mode prompt as
// waiting). When tmux is reachable, it also requires the input box to be drawn
// without an open dialog or running turn.
func (a Aoe) waitReady(ctx context.Context, id string, poll time.Duration) error {
	var lastStatus string
	var lastErr error
	for {
		s, err := a.Show(ctx, id)
		if err != nil {
			lastErr = err
		} else {
			lastStatus, lastErr = s.Status, nil
			switch s.Status {
			case "idle":
				if a.paneAcceptsInput(ctx, id) {
					return nil
				}
			case "waiting":
				if s.runsAntigravity() && a.paneAcceptsInput(ctx, id) {
					return nil
				}
			case "error":
				return fmt.Errorf("session %s entered error state", id)
			}
		}
		select {
		case <-ctx.Done():
			if lastStatus != "" {
				return fmt.Errorf("session did not become ready (status: %s): %w", lastStatus, ctx.Err())
			}
			if lastErr != nil {
				return fmt.Errorf("session did not become ready (%v): %w", lastErr, ctx.Err())
			}
			return fmt.Errorf("session did not become ready: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// paneAcceptsInput checks the session's tmux pane when one can be captured so
// a stale idle status before the TUI draws its prompt (or an open dialog) does
// not pass as ready. If tmux is unavailable, aoe's status stands.
func (a Aoe) paneAcceptsInput(ctx context.Context, id string) bool {
	pane, err := a.pane(ctx, id)
	if err != nil {
		return true
	}
	screen, err := a.Run(ctx, "", "tmux", "capture-pane", "-p", "-t", pane)
	if err != nil {
		return true
	}
	return paneReady(screen)
}

func paneReady(screen []byte) bool {
	lines := bytes.Split(screen, []byte("\n"))
	var bottom [][]byte
	for i := len(lines) - 1; i >= 0 && len(bottom) < 12; i-- {
		if trimmed := bytes.TrimSpace(lines[i]); len(trimmed) > 0 {
			bottom = append(bottom, trimmed)
		}
	}
	rules := 0
	for _, line := range bottom {
		if bytes.Contains(line, []byte(dialogHint)) || bytes.Contains(line, []byte("esc to cancel")) {
			return false
		}
		if bytes.Contains(line, []byte("────────")) {
			rules++
		}
	}
	return rules >= 2
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

// Add creates a stopped session in dir that runs tool, aoe's name for it,
// with args after the tool's command, and returns its id; Send starts it.
func (a Aoe) Add(ctx context.Context, dir, title, group, tool, args string) (string, error) {
	cmd := []string{"add", dir, "-t", title, "-g", group, "--tool", tool}
	if args != "" {
		cmd = append(cmd, "--extra-args", args)
	}
	if _, err := a.cmd(ctx, cmd...); err != nil {
		return "", err
	}
	s, err := a.Show(ctx, title)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// keptMark separates a worker's title from the ID in KeptTitle.
const keptMark = "~"

// KeptTitle names a worker's old session after it moved to another tool.
// aoe keeps it, but peyk shows one worker per project, so Sessions leaves it
// out.
func KeptTitle(id, title string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return title + keptMark + id
}

func isKept(title string) bool { return strings.Contains(title, keptMark) }

// Rename gives a session a new title.
func (a Aoe) Rename(ctx context.Context, id, title string) error {
	_, err := a.cmd(ctx, "session", "rename", id, "-t", title)
	return err
}
