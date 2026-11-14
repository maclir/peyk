package laptop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maclir/peyk/internal/session"
)

// SaveUsage writes u to path unless the file already holds it. The status
// line runs after every assistant message; leaving an unchanged file alone
// keeps the agent's watcher quiet.
func SaveUsage(path string, u *session.Usage) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Several sessions' status lines can write at once; a rename never leaves
	// the agent reading half a file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadUsage reads what SaveUsage wrote; nil when nothing has been saved yet.
func LoadUsage(path string) (*session.Usage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u session.Usage
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return &u, nil
}
