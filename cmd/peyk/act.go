package main

import (
	"context"
	"time"

	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/session"
)

// actions carries out commands with aoe and git.
type actions struct {
	aoe   laptop.Aoe
	group string
}

func (a actions) Send(ctx context.Context, session, text string) error {
	return a.aoe.Send(ctx, session, text)
}

func (a actions) Create(ctx context.Context, dir, title string) (string, error) {
	return a.aoe.Create(ctx, dir, title, a.group)
}

// clearWithin bounds the wait for a cleared session's new conversation, which
// Claude Code starts in well under a second.
const clearWithin = 20 * time.Second

func (a actions) Clear(ctx context.Context, session string) error {
	ctx, cancel := context.WithTimeout(ctx, clearWithin)
	defer cancel()
	return a.aoe.Clear(ctx, session, 250*time.Millisecond)
}

// answerWithin bounds the wait for Claude Code to record a dismissed dialog.
const answerWithin = 10 * time.Second

func (a actions) Answer(ctx context.Context, session string, t session.Transcript, question, text string) error {
	ctx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	return a.aoe.Answer(ctx, session, t, question, text, 100*time.Millisecond)
}

func (a actions) Stop(ctx context.Context, session string) error { return a.aoe.Stop(ctx, session) }

func (a actions) Push(ctx context.Context, dir string) error {
	return laptop.Push(ctx, laptop.Exec, dir)
}
