package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/store"
)

type subscriptionStore interface {
	Subscriptions(context.Context) ([]notify.Subscription, error)
	Delete(ctx context.Context, collection, id string) error
}

// notifier sends each note to every subscribed phone. A subscription the
// push service no longer knows is deleted; any other failure is returned.
type notifier struct {
	store  subscriptionStore
	sender interface {
		Send(context.Context, notify.Subscription, notify.Note) error
	}
}

func (n notifier) Notify(ctx context.Context, notes []notify.Note) error {
	subs, err := n.store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		for _, note := range notes {
			err := n.sender.Send(ctx, sub, note)
			if errors.Is(err, notify.ErrGone) {
				if err := n.store.Delete(ctx, store.Subscriptions, sub.ID); err != nil {
					return err
				}
				break
			}
			if err != nil {
				return fmt.Errorf("notify %q: %w", note.Title, err)
			}
		}
	}
	return nil
}

func vapidKeys() (notify.Keys, error) {
	home, _ := os.UserHomeDir()
	return notify.LoadOrCreateKeys(filepath.Join(home, ".peyk", "vapid.json"))
}
