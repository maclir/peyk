// Package store is where the laptop publishes what the phone reads, and where
// the phone leaves tasks and commands for the laptop.
//
// Layout:
//
//	sessions/{aoe session id}  session.Snapshot     agent writes
//	projects/{name}            session.Project      agent writes
//	laptop/status              Laptop               agent writes
//	tasks/{id}                 Task                 phone creates, agent updates
//	commands/{id}              command.Command      phone creates, agent updates
//	pairings/{code}            Pairing              phone creates, agent deletes
//	subscriptions/{id}         notify.Subscription  phone creates, agent deletes when gone
//	laptop/push                {vapid_public_key}   agent writes
//	laptop/usage               session.Usage        agent writes
//	attachments/{sha256}       Attachment           phone creates and deletes
package store

import (
	"context"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/notify"
)

const (
	Sessions      = "sessions"
	Projects      = "projects"
	Tasks         = "tasks"
	Commands      = "commands"
	Pairings      = "pairings"
	Subscriptions = "subscriptions"
	Attachments   = "attachments"
)

// Attachment is an image added to a task. Its id is the SHA-256 of Data in
// hex, so whoever reads it can tell it is the image that was signed for.
type Attachment struct {
	Data      []byte    `firestore:"data"`
	Type      string    `firestore:"type"`
	CreatedAt time.Time `firestore:"created_at"`
}

// AttachmentReader fetches a task's images.
type AttachmentReader interface {
	Attachment(ctx context.Context, id string) (Attachment, error)
}

// Pairing is the public half of a passkey the phone just created.
type Pairing struct {
	CredentialID string    `firestore:"credential_id"`
	PublicKey    string    `firestore:"public_key"`
	Alg          int       `firestore:"alg"`
	CreatedAt    time.Time `firestore:"created_at"`
}

// Incoming is a pending command as it arrives.
type Incoming struct {
	ID      string
	Command command.Command
}

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
	DoneAt       time.Time `firestore:"done_at,omitempty" json:"done_at,omitempty"`
	// Commit is the session's last commit when the task finished, if it was
	// made after the task was sent.
	Commit string `firestore:"commit,omitempty" json:"commit,omitempty"`
	// Images are attachment ids.
	Images []string `firestore:"images,omitempty" json:"images,omitempty"`
	// Rank is the phone's order for queued tasks.
	Rank float64 `firestore:"rank,omitempty" json:"rank,omitempty"`
}

const (
	TaskQueued     = "queued"
	TaskDispatched = "dispatched"
	TaskDone       = "done"
)

type Store interface {
	Put(ctx context.Context, collection, id string, v any) error
	Update(ctx context.Context, collection, id string, fields map[string]any) error
	Delete(ctx context.Context, collection, id string) error
	IDs(ctx context.Context, collection string) ([]string, error)
	SetLaptop(context.Context, Laptop) error
	Close() error
}

// Listener delivers what the phone leaves for the laptop.
type Listener interface {
	// Commands sends every pending command, including ones already waiting
	// in the order they were made, until ctx ends or listening fails.
	Commands(ctx context.Context, out chan<- Incoming) error
	// Pairing waits for the phone to write the pairing with this code.
	Pairing(ctx context.Context, code string) (Pairing, error)
	Subscriptions(ctx context.Context) ([]notify.Subscription, error)
}

// HeldReader finds the replies held for the phone to send again or cancel.
type HeldReader interface {
	HeldReplies(ctx context.Context) (map[string]command.Command, error)
}

// TaskReader finds tasks the agent is waiting on.
type TaskReader interface {
	DispatchedTasks(ctx context.Context) (map[string]Task, error)
}
