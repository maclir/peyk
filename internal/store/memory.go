package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/session"
)

// Memory is the store for tests and local runs.
type Memory struct {
	mu       sync.Mutex
	docs     map[string]map[string]any
	updates  map[string]map[string]map[string]any
	laptop   Laptop
	writes   int
	commands chan Incoming
	pairings chan Pairing
}

func NewMemory() *Memory {
	return &Memory{
		docs: map[string]map[string]any{}, updates: map[string]map[string]map[string]any{},
		commands: make(chan Incoming, 16), pairings: make(chan Pairing, 1),
	}
}

func (m *Memory) Update(_ context.Context, collection, id string, fields map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updates[collection] == nil {
		m.updates[collection] = map[string]map[string]any{}
	}
	if m.updates[collection][id] == nil {
		m.updates[collection][id] = map[string]any{}
	}
	for k, v := range fields {
		m.updates[collection][id][k] = v
	}
	m.writes++
	return nil
}

// Updated returns the fields Update set on a document.
func (m *Memory) Updated(collection, id string) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updates[collection][id]
}

// Send queues a command as if the phone had written it.
func (m *Memory) Send(in Incoming) {
	m.mu.Lock()
	if m.docs[Commands] == nil {
		m.docs[Commands] = map[string]any{}
	}
	m.docs[Commands][in.ID] = in.Command
	m.mu.Unlock()
	m.commands <- in
}

// Pair answers a waiting Pairing call as if the phone had written it.
func (m *Memory) Pair(p Pairing) { m.pairings <- p }

func (m *Memory) Commands(ctx context.Context, out chan<- Incoming) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case in := <-m.commands:
			select {
			case out <- in:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (m *Memory) Pairing(ctx context.Context, _ string) (Pairing, error) {
	select {
	case p := <-m.pairings:
		return p, nil
	case <-ctx.Done():
		return Pairing{}, ctx.Err()
	}
}

func (m *Memory) Put(_ context.Context, collection, id string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs[collection] == nil {
		m.docs[collection] = map[string]any{}
	}
	m.docs[collection][id] = v
	m.writes++
	return nil
}

func (m *Memory) Delete(_ context.Context, collection, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs[collection], id)
	m.writes++
	return nil
}

func (m *Memory) IDs(_ context.Context, collection string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.docs[collection]))
	for id := range m.docs[collection] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Memory) SetLaptop(_ context.Context, l Laptop) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.laptop = l
	m.writes++
	return nil
}

// Get returns one stored document.
func (m *Memory) Get(collection, id string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.docs[collection][id]
	return v, ok
}

// Laptop returns the stored laptop status.
func (m *Memory) Laptop() Laptop {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.laptop
}

// Writes counts every write, so tests can prove unchanged state is not rewritten.
func (m *Memory) Writes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

func (m *Memory) Close() error { return nil }

func (m *Memory) Attachment(_ context.Context, id string) (Attachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.docs[Attachments][id].(Attachment)
	if !ok {
		return Attachment{}, fmt.Errorf("no attachment %s", id)
	}
	return a, nil
}

func (m *Memory) Subscriptions(context.Context) ([]notify.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []notify.Subscription
	for id, v := range m.docs[Subscriptions] {
		sub := v.(notify.Subscription)
		sub.ID = id
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// HeldReplies applies Update's status to commands Send stored.
func (m *Memory) NotifiedSessions(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, v := range m.docs[Sessions] {
		if s, ok := v.(session.Snapshot); ok && s.Notified {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Memory) HeldReplies(context.Context) (map[string]command.Command, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]command.Command{}
	for id, v := range m.docs[Commands] {
		c := v.(command.Command)
		if st, ok := m.updates[Commands][id]["status"].(string); ok {
			c.Status = st
		}
		if c.Status == command.Held {
			out[id] = c
		}
	}
	return out, nil
}

// Ideas applies Update's changes to stored ideas, as Firestore would.
func (m *Memory) Ideas(_ context.Context, project string) ([]idea.Idea, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []idea.Idea
	for id, v := range m.docs[Ideas] {
		i := v.(idea.Idea)
		u := m.updates[Ideas][id]
		if st, ok := u["status"].(string); ok {
			i.Status = st
		}
		if n, ok := u["note"].(string); ok {
			i.Note = n
		}
		if i.Project == project {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.Before(out[b].CreatedAt)
		}
		return out[a].Title < out[b].Title
	})
	return out, nil
}

// DispatchedTasks applies Update's changes to stored tasks, as Firestore would.
func (m *Memory) DispatchedTasks(context.Context) (map[string]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Task{}
	for id, v := range m.docs[Tasks] {
		t := v.(Task)
		u := m.updates[Tasks][id]
		if st, ok := u["status"].(string); ok {
			t.Status = st
		}
		if at, ok := u["dispatched_at"].(time.Time); ok {
			t.DispatchedAt = at
		}
		if sid, ok := u["session_id"].(string); ok {
			t.SessionID = sid
		}
		if cid, ok := u["conversation_id"].(string); ok {
			t.ConversationID = cid
		}
		if t.Status == TaskDispatched {
			out[id] = t
		}
	}
	return out, nil
}
