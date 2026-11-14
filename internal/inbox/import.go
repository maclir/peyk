// Package inbox brings tasks in from elsewhere.
package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

// Item is one task to import. Project may be a project's name or its
// directory name, in any case.
type Item struct {
	Project   string    `json:"project"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Import adds every item as a queued task. It writes nothing unless every
// item names a known project, so a typo cannot leave half an import behind.
// Task ids derive from the content, so running the same import twice adds
// nothing new.
func Import(ctx context.Context, r io.Reader, projects []session.Project, s store.Store) (int, error) {
	var items []Item
	if err := json.NewDecoder(r).Decode(&items); err != nil {
		return 0, fmt.Errorf("read tasks: %w", err)
	}
	lookup := map[string]string{}
	for _, p := range projects {
		lookup[strings.ToLower(p.Name)] = p.Name
		lookup[strings.ToLower(filepath.Base(p.Path))] = p.Name
	}
	tasks := make([]store.Task, 0, len(items))
	unknown := map[string]bool{}
	for _, it := range items {
		name, ok := lookup[strings.ToLower(strings.TrimSpace(it.Project))]
		if !ok {
			unknown[it.Project] = true
			continue
		}
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		tasks = append(tasks, store.Task{Project: name, Text: text, CreatedAt: it.CreatedAt, Status: store.TaskQueued})
	}
	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for n := range unknown {
			names = append(names, n)
		}
		sort.Strings(names)
		return 0, fmt.Errorf("no project named %s; nothing imported", strings.Join(names, ", "))
	}
	for i, t := range tasks {
		if err := s.Put(ctx, store.Tasks, taskID(t), t); err != nil {
			return i, err
		}
	}
	return len(tasks), nil
}

func taskID(t store.Task) string {
	sum := sha256.Sum256([]byte(t.Project + "\x00" + t.Text + "\x00" + t.CreatedAt.UTC().Format(time.RFC3339Nano)))
	return "import-" + hex.EncodeToString(sum[:10])
}
