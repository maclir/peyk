package laptop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HiddenFile keeps the projects hidden from the phone's picker as a JSON
// list of names.
type HiddenFile struct{ Path string }

// Load returns the hidden names; a missing file hides nothing.
func (f HiddenFile) Load() ([]string, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	return names, nil
}

// Save replaces the list in one rename, so a crash leaves the old list or the
// new one.
func (f HiddenFile) Save(names []string) error {
	if names == nil {
		names = []string{}
	}
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}
