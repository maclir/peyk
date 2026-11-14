package store

import (
	"context"
	"sort"
	"sync"

	"github.com/maclir/peyk/internal/idea"
)

type Memory struct {
	mu    sync.RWMutex
	ideas map[string]idea.Idea
}

func NewMemory() *Memory { return &Memory{ideas: make(map[string]idea.Idea)} }

func (m *Memory) Create(_ context.Context, v idea.Idea) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ideas[v.ID] = v
	return nil
}

func (m *Memory) List(_ context.Context) ([]idea.Idea, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]idea.Idea, 0, len(m.ideas))
	for _, v := range m.ideas {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// MemoryAdminKey opens the dashboard on the in-memory store, which only ever
// backs local runs and tests.
const MemoryAdminKey = "test-admin-key"

func (m *Memory) AdminKey(context.Context) (string, error) { return MemoryAdminKey, nil }

func (m *Memory) Close() error { return nil }
