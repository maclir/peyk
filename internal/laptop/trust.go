package laptop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Trust marks dir as trusted in Claude Code's global config at path. Claude
// Code asks whether to trust a folder the first time it opens there, in a
// terminal dialog no transcript records, so a task sent to a new worker would
// otherwise be typed into that dialog. Every other setting keeps its value.
func Trust(path, dir string) error {
	cfg := map[string]json.RawMessage{}
	mode := fs.FileMode(0o600)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	projects := map[string]map[string]json.RawMessage{}
	if p, ok := cfg["projects"]; ok {
		if err := json.Unmarshal(p, &projects); err != nil {
			return fmt.Errorf("decode projects in %s: %w", path, err)
		}
	}
	project := projects[dir]
	if string(project["hasTrustDialogAccepted"]) == "true" {
		return nil
	}
	if project == nil {
		project = map[string]json.RawMessage{}
	}
	project["hasTrustDialogAccepted"] = json.RawMessage("true")
	projects[dir] = project
	if cfg["projects"], err = json.Marshal(projects); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Claude Code rewrites this file while it runs, so the new contents
	// replace the old in one rename rather than through a partial write.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.peyk-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
