package store

import (
	"context"
	"sort"
	"sync"

	"github.com/maclir/peyk/internal/session"
)

// Memory is the store for tests and local runs.
type Memory struct {
	mu       sync.Mutex
	Sessions map[string]session.Snapshot
	Laptop   Laptop
	// Writes counts every write, so tests can prove unchanged state is not rewritten.
	Writes int
}

func NewMemory() *Memory { return &Memory{Sessions: map[string]session.Snapshot{}} }

func (m *Memory) PutSession(_ context.Context, s session.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sessions[s.ID] = s
	m.Writes++
	return nil
}

func (m *Memory) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Sessions, id)
	m.Writes++
	return nil
}

func (m *Memory) SessionIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.Sessions))
	for id := range m.Sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Memory) SetLaptop(_ context.Context, l Laptop) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Laptop = l
	m.Writes++
	return nil
}

// Snapshot returns a copy of the stored state.
func (m *Memory) Snapshot() (map[string]session.Snapshot, Laptop, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]session.Snapshot, len(m.Sessions))
	for k, v := range m.Sessions {
		out[k] = v
	}
	return out, m.Laptop, m.Writes
}

func (m *Memory) Close() error { return nil }
