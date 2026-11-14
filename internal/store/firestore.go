package store

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
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
