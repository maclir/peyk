package laptop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// ErrAnswered means the dialog was answered at the terminal first.
var ErrAnswered = errors.New("the question was already answered on the laptop")

// dialogHint is the key hint Claude Code prints under an open dialog.
const dialogHint = "Esc to cancel"

// Answer gives a session's open AskUserQuestion dialog the answer in text.
// Claude Code's dialog cannot be filled in from outside, so Answer dismisses
// it with Esc, which ends the turn waiting for the user, then sends text as a
// prompt. The terminal can answer up to the moment Esc is pressed.
func (a Aoe) Answer(ctx context.Context, id string, t session.Transcript, question, text string, poll time.Duration) error {
	if q := PendingQuestion(t); q == nil || q.ID != question {
		return ErrAnswered
	}
	pane, err := a.pane(ctx, id)
	if err != nil {
		return err
	}
	screen, err := a.Run(ctx, "", "tmux", "capture-pane", "-p", "-t", pane)
	if err != nil {
		return err
	}
	// Esc anywhere but the dialog would interrupt the agent's turn instead.
	if !bytes.Contains(screen, []byte(dialogHint)) {
		return errors.New("the question dialog is not on the laptop's screen")
	}
	if _, err := a.Run(ctx, "", "tmux", "send-keys", "-t", pane, "Escape"); err != nil {
		return err
	}
	for {
		done, refused := Outcome(t, question)
		if done && !refused {
			return ErrAnswered
		}
		if done {
			return a.Deliver(ctx, id, t, text, 0, poll)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the dialog did not close: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// pane finds the tmux session aoe runs a session in. aoe names it
// aoe_<title>_<first 8 characters of the id>.
func (a Aoe) pane(ctx context.Context, id string) (string, error) {
	out, err := a.Run(ctx, "", "tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return "", err
	}
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(name, "aoe_") && strings.HasSuffix(name, "_"+short) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no tmux session for %s", id)
}
