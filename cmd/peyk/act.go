package main

import (
	"context"
	"fmt"
	"time"

	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/session"
)

// actions carries out commands with aoe and git.
type actions struct {
	aoe   laptop.Aoe
	group string
	// claudeConfig is Claude Code's global config, where folder trust is kept.
	claudeConfig string
	// antigravitySettings is Antigravity CLI's settings.json, where workspace trust is kept.
	antigravitySettings string
}

func (a actions) Send(ctx context.Context, session, text string) error {
	return a.aoe.Send(ctx, session, text)
}

func (a actions) Create(ctx context.Context, dir, title string) (string, error) {
	if err := laptop.Trust(a.claudeConfig, dir); err != nil {
		return "", fmt.Errorf("trust %s for Claude Code: %w", dir, err)
	}
	return a.aoe.Create(ctx, dir, title, a.group)
}

// clearWithin bounds the wait for a cleared session's new conversation, which
// Claude Code starts in well under a second.
const clearWithin = 20 * time.Second

func (a actions) Clear(ctx context.Context, session string, t session.Transcript) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, clearWithin)
	defer cancel()
	return a.aoe.Clear(ctx, session, t, 250*time.Millisecond)
}

// answerWithin bounds the wait for Claude Code to record a dismissed dialog.
const answerWithin = 10 * time.Second

func (a actions) Answer(ctx context.Context, session string, t session.Transcript, question, text string) error {
	ctx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	return a.aoe.Answer(ctx, session, t, question, text, 100*time.Millisecond)
}

func (a actions) Stop(ctx context.Context, session string) error { return a.aoe.Stop(ctx, session) }

func (a actions) Start(ctx context.Context, session string) error { return a.aoe.Start(ctx, session) }

func (a actions) Restart(ctx context.Context, session string) error {
	return a.aoe.Restart(ctx, session)
}

func (a actions) Resume(ctx context.Context, session string, t session.Transcript, conversation string, stopped bool) error {
	return a.aoe.Resume(ctx, session, t, conversation, stopped)
}

func (a actions) Push(ctx context.Context, dir string) error {
	return laptop.Push(ctx, laptop.Exec, dir)
}

// Swap moves a worker to another tool on a fresh conversation. The old session
// is kept aside under laptop.KeptTitle and stopped once the new one exists, so
// peyk shows only the new worker while leaving the old transcript on disk for
// a context handoff. If the new session cannot be added, the old one keeps its
// title and state.
func (a actions) Swap(ctx context.Context, s session.Snapshot, tool, model string) (string, error) {
	switch tool {
	case "":
		if err := laptop.Trust(a.claudeConfig, s.Path); err != nil {
			return "", fmt.Errorf("trust %s for Claude Code: %w", s.Path, err)
		}
	case session.Antigravity:
		if err := laptop.TrustAntigravity(a.antigravitySettings, s.Path); err != nil {
			return "", fmt.Errorf("trust %s for Antigravity: %w", s.Path, err)
		}
	}
	if err := a.aoe.Rename(ctx, s.ID, laptop.KeptTitle(s.ID, s.Title)); err != nil {
		return "", err
	}
	aoeTool := tool
	if aoeTool == "" {
		aoeTool = "claude"
	}
	id, err := a.aoe.Add(ctx, s.Path, s.Title, a.group, aoeTool, "--model "+model)
	if err != nil {
		if rerr := a.aoe.Rename(ctx, s.ID, s.Title); rerr != nil {
			return "", fmt.Errorf("start %s on %s: %w; the old session is still named %s: %v", s.Title, model, err, laptop.KeptTitle(s.ID, s.Title), rerr)
		}
		return "", fmt.Errorf("start %s on %s: %w", s.Title, model, err)
	}
	if s.AoeStatus != "stopped" {
		if err := a.aoe.Stop(ctx, s.ID); err != nil {
			return id, fmt.Errorf("stop previous %s session: %w", s.Title, err)
		}
	}
	return id, nil
}
