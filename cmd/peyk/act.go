package main

import (
	"context"

	"github.com/maclir/peyk/internal/laptop"
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

func (a actions) Stop(ctx context.Context, session string) error { return a.aoe.Stop(ctx, session) }

func (a actions) Push(ctx context.Context, dir string) error {
	return laptop.Push(ctx, laptop.Exec, dir)
}
