package store

import (
	"context"
	"errors"

	"github.com/maclir/peyk/internal/idea"
)

// ErrNoAdminKey means config/admin holds no key, which locks the dashboard.
var ErrNoAdminKey = errors.New("admin key not configured")

type Store interface {
	Create(context.Context, idea.Idea) error
	List(context.Context) ([]idea.Idea, error)
	// AdminKey returns the secret that opens the dashboard. It is read on every
	// request, so replacing the document rotates the key without a deploy.
	AdminKey(context.Context) (string, error)
	Close() error
}
