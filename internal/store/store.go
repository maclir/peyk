// Package store is where the laptop publishes what the phone reads.
//
// Layout:
//
//	sessions/{aoe session id}  session.Snapshot
//	laptop/status              Laptop
package store

import (
	"context"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Laptop says whether the agent is running. SeenAt lets the phone tell a
// stopped agent from one on a laptop that went to sleep without saying so.
type Laptop struct {
	Online bool      `firestore:"online" json:"online"`
	SeenAt time.Time `firestore:"seen_at" json:"seen_at"`
}

type Store interface {
	PutSession(context.Context, session.Snapshot) error
	DeleteSession(ctx context.Context, id string) error
	SessionIDs(context.Context) ([]string, error)
	SetLaptop(context.Context, Laptop) error
	Close() error
}
