package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Health is what the menu bar app shows about the running agent. launchd
// knows only that a process is alive; this says it got as far as publishing.
type Health struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	RefreshedAt time.Time `json:"refreshed_at,omitzero"`
	Workers     int       `json:"workers"`
	// Unreachable is why the last write of the laptop's status failed. The
	// phone reads that status, so while this is set the phone shows the
	// laptop as offline even though the process is alive.
	Unreachable string `json:"unreachable,omitempty"`
}

// WriteHealth replaces the file whole, so a reader never sees half of it.
func WriteHealth(path string, h Health) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".health-*")
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

// RemoveHealth says the agent is gone. A file left by an agent that was
// killed names a dead pid, which the menu bar app checks for.
func RemoveHealth(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
