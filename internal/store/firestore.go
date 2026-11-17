package store

import (
	"context"
	"fmt"
	"sort"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/notify"
)

type Firestore struct{ client *firestore.Client }

func NewFirestore(ctx context.Context, projectID string, opts ...option.ClientOption) (*Firestore, error) {
	client, err := firestore.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("create Firestore client: %w", err)
	}
	return &Firestore{client: client}, nil
}

func (s *Firestore) Put(ctx context.Context, collection, id string, v any) error {
	if _, err := s.client.Collection(collection).Doc(id).Set(ctx, v); err != nil {
		return fmt.Errorf("write %s/%s: %w", collection, id, err)
	}
	return nil
}

func (s *Firestore) Update(ctx context.Context, collection, id string, fields map[string]any) error {
	updates := make([]firestore.Update, 0, len(fields))
	for k, v := range fields {
		updates = append(updates, firestore.Update{Path: k, Value: v})
	}
	if _, err := s.client.Collection(collection).Doc(id).Update(ctx, updates); err != nil {
		return fmt.Errorf("update %s/%s: %w", collection, id, err)
	}
	return nil
}

// Commands listens for pending commands over one streaming connection.
// A document leaves the query once the agent marks it done or failed.
func (s *Firestore) Commands(ctx context.Context, out chan<- Incoming) error {
	it := s.client.Collection(Commands).Where("status", "==", command.Pending).Snapshots(ctx)
	defer it.Stop()
	for {
		snap, err := it.Next()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("listen for commands: %w", err)
		}
		var added []Incoming
		for _, ch := range snap.Changes {
			if ch.Kind != firestore.DocumentAdded {
				continue
			}
			var c command.Command
			if err := ch.Doc.DataTo(&c); err != nil {
				return fmt.Errorf("decode command %s: %w", ch.Doc.Ref.ID, err)
			}
			added = append(added, Incoming{ID: ch.Doc.Ref.ID, Command: c})
		}
		// Replies queued while the laptop was away arrive together and
		// must reach their session in the order they were written.
		sort.SliceStable(added, func(i, j int) bool { return added[i].Command.CreatedAt.Before(added[j].Command.CreatedAt) })
		for _, in := range added {
			select {
			case out <- in:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// Pairing waits until the phone writes pairings/{code}.
func (s *Firestore) Pairing(ctx context.Context, code string) (Pairing, error) {
	it := s.client.Collection(Pairings).Doc(code).Snapshots(ctx)
	defer it.Stop()
	for {
		snap, err := it.Next()
		if err != nil {
			return Pairing{}, fmt.Errorf("wait for pairing: %w", err)
		}
		if !snap.Exists() {
			continue
		}
		var p Pairing
		if err := snap.DataTo(&p); err != nil {
			return Pairing{}, fmt.Errorf("decode pairing: %w", err)
		}
		return p, nil
	}
}

func (s *Firestore) DispatchedTasks(ctx context.Context) (map[string]Task, error) {
	docs, err := s.client.Collection(Tasks).Where("status", "==", TaskDispatched).Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list dispatched tasks: %w", err)
	}
	out := make(map[string]Task, len(docs))
	for _, d := range docs {
		var t Task
		if err := d.DataTo(&t); err != nil {
			return nil, fmt.Errorf("decode task %s: %w", d.Ref.ID, err)
		}
		out[d.Ref.ID] = t
	}
	return out, nil
}

func (s *Firestore) HeldReplies(ctx context.Context) (map[string]command.Command, error) {
	docs, err := s.client.Collection(Commands).Where("status", "==", command.Held).Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list held replies: %w", err)
	}
	out := make(map[string]command.Command, len(docs))
	for _, d := range docs {
		var c command.Command
		if err := d.DataTo(&c); err != nil {
			return nil, fmt.Errorf("decode command %s: %w", d.Ref.ID, err)
		}
		out[d.Ref.ID] = c
	}
	return out, nil
}

func (s *Firestore) Attachment(ctx context.Context, id string) (Attachment, error) {
	doc, err := s.client.Collection(Attachments).Doc(id).Get(ctx)
	if err != nil {
		return Attachment{}, fmt.Errorf("read attachment %s: %w", id, err)
	}
	var a Attachment
	if err := doc.DataTo(&a); err != nil {
		return Attachment{}, fmt.Errorf("decode attachment %s: %w", id, err)
	}
	return a, nil
}

func (s *Firestore) Subscriptions(ctx context.Context) ([]notify.Subscription, error) {
	docs, err := s.client.Collection(Subscriptions).Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := make([]notify.Subscription, 0, len(docs))
	for _, d := range docs {
		var sub notify.Subscription
		if err := d.DataTo(&sub); err != nil {
			return nil, fmt.Errorf("decode subscription %s: %w", d.Ref.ID, err)
		}
		sub.ID = d.Ref.ID
		out = append(out, sub)
	}
	return out, nil
}

func (s *Firestore) Delete(ctx context.Context, collection, id string) error {
	if _, err := s.client.Collection(collection).Doc(id).Delete(ctx); err != nil {
		return fmt.Errorf("delete %s/%s: %w", collection, id, err)
	}
	return nil
}

func (s *Firestore) IDs(ctx context.Context, collection string) ([]string, error) {
	refs, err := s.client.Collection(collection).DocumentRefs(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", collection, err)
	}
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	return ids, nil
}

func (s *Firestore) SetLaptop(ctx context.Context, l Laptop) error {
	if _, err := s.client.Collection("laptop").Doc("status").Set(ctx, l); err != nil {
		return fmt.Errorf("write laptop status: %w", err)
	}
	return nil
}

func (s *Firestore) Close() error { return s.client.Close() }
