// Package store is where the laptop publishes what the phone reads, and where
// the phone leaves tasks and commands for the laptop.
//
// Layout:
//
//	sessions/{aoe session id}  session.Snapshot     agent writes
//	projects/{name}            session.Project      agent writes
//	laptop/status              Laptop               agent writes
//	tasks/{id}                 Task                 phone creates, agent updates
package store

import (
	"context"
	"time"
)

const (
	Sessions = "sessions"
	Projects = "projects"
	Tasks    = "tasks"
)

// Laptop says whether the agent is running. SeenAt lets the phone tell a
// stopped agent from one on a laptop that went to sleep without saying so.
type Laptop struct {
	Online bool      `firestore:"online" json:"online"`
	SeenAt time.Time `firestore:"seen_at" json:"seen_at"`
}

// Task is an inbox item.
type Task struct {
	Project      string    `firestore:"project" json:"project"`
	Text         string    `firestore:"text" json:"text"`
	CreatedAt    time.Time `firestore:"created_at" json:"created_at"`
	Status       string    `firestore:"status" json:"status"`
	DispatchedAt time.Time `firestore:"dispatched_at,omitempty" json:"dispatched_at,omitempty"`
	SessionID    string    `firestore:"session_id,omitempty" json:"session_id,omitempty"`
}

const (
	TaskQueued     = "queued"
	TaskDispatched = "dispatched"
)

type Store interface {
	Put(ctx context.Context, collection, id string, v any) error
	Delete(ctx context.Context, collection, id string) error
	IDs(ctx context.Context, collection string) ([]string, error)
	SetLaptop(context.Context, Laptop) error
	Close() error
}
