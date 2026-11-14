package store

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/session"
)

type Firestore struct{ client *firestore.Client }

func NewFirestore(ctx context.Context, projectID string, opts ...option.ClientOption) (*Firestore, error) {
	client, err := firestore.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("create Firestore client: %w", err)
	}
	return &Firestore{client: client}, nil
}

func (s *Firestore) PutSession(ctx context.Context, v session.Snapshot) error {
	if _, err := s.client.Collection("sessions").Doc(v.ID).Set(ctx, v); err != nil {
		return fmt.Errorf("write session %s: %w", v.ID, err)
	}
	return nil
}

func (s *Firestore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.client.Collection("sessions").Doc(id).Delete(ctx); err != nil {
		return fmt.Errorf("delete session %s: %w", id, err)
	}
	return nil
}

func (s *Firestore) SessionIDs(ctx context.Context) ([]string, error) {
	refs, err := s.client.Collection("sessions").DocumentRefs(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
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
