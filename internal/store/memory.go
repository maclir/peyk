package store

import (
	"context"
	"sort"
	"sync"
)

// Memory is the store for tests and local runs.
type Memory struct {
	mu     sync.Mutex
	docs   map[string]map[string]any
	laptop Laptop
	writes int
}

func NewMemory() *Memory { return &Memory{docs: map[string]map[string]any{}} }

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
