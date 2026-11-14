//go:build !darwin

package agent

import "context"

// EditWatcher needs FSEvents; elsewhere it watches nothing, and only hooks
// and the git watcher report changes.
type EditWatcher struct{}

func NewEditWatcher(chan<- struct{}) *EditWatcher { return &EditWatcher{} }

func (*EditWatcher) Set(context.Context, []string) error { return nil }

func (*EditWatcher) Close() {}
