package store

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/command"
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
		for _, ch := range snap.Changes {
			if ch.Kind != firestore.DocumentAdded {
				continue
			}
			var c command.Command
			if err := ch.Doc.DataTo(&c); err != nil {
				return fmt.Errorf("decode command %s: %w", ch.Doc.Ref.ID, err)
			}
			select {
			case out <- Incoming{ID: ch.Doc.Ref.ID, Command: c}:
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
