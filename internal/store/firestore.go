package store

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/maclir/peyk/internal/idea"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const collection = "ideas"

type Firestore struct{ client *firestore.Client }

func NewFirestore(ctx context.Context, projectID string) (*Firestore, error) {
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("create Firestore client: %w", err)
	}
	return &Firestore{client: client}, nil
}

func (s *Firestore) Create(ctx context.Context, v idea.Idea) error {
	if _, err := s.client.Collection(collection).Doc(v.ID).Create(ctx, v); err != nil {
		return fmt.Errorf("create idea: %w", err)
	}
	return nil
}

func (s *Firestore) List(ctx context.Context) ([]idea.Idea, error) {
	docs, err := s.client.Collection(collection).OrderBy("created_at", firestore.Desc).Limit(100).Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list ideas: %w", err)
	}
	out := make([]idea.Idea, 0, len(docs))
	for _, doc := range docs {
		var v idea.Idea
		if err := doc.DataTo(&v); err != nil {
			return nil, fmt.Errorf("decode idea %s: %w", doc.Ref.ID, err)
		}
		v.ID = doc.Ref.ID
		out = append(out, v)
	}
	return out, nil
}

// AdminKey reads config/admin's key field. The document is created by hand in
// the console; nothing in the app writes it.
func (s *Firestore) AdminKey(ctx context.Context) (string, error) {
	doc, err := s.client.Collection("config").Doc("admin").Get(ctx)
	if status.Code(err) == codes.NotFound {
		return "", ErrNoAdminKey
	}
	if err != nil {
		return "", fmt.Errorf("read admin key: %w", err)
	}
	key, _ := doc.Data()["key"].(string)
	if key == "" {
		return "", ErrNoAdminKey
	}
	return key, nil
}

func (s *Firestore) Close() error { return s.client.Close() }
