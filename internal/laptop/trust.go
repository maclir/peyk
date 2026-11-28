package laptop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Trust marks dir as trusted in Claude Code's global config at path. Claude
// Code asks whether to trust a folder the first time it opens there, in a
// terminal dialog no transcript records, so a task sent to a new worker would
// otherwise be typed into that dialog. Every other setting keeps its value.
func Trust(path, dir string) error {
	cfg, mode, err := readConfig(path)
	if err != nil {
		return err
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
	return writeConfig(path, cfg, mode, ".claude.json.peyk-*")
}

// TrustAntigravity marks dir in trustedWorkspaces inside Antigravity CLI's
// settings.json at path. Antigravity asks whether to trust a folder the first
// time it opens there, so an initial catch-up prompt sent over aoe would
// otherwise be swallowed by that dialog. Every other setting keeps its value.
func TrustAntigravity(path, dir string) error {
	cfg, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	var workspaces []string
	if w, ok := cfg["trustedWorkspaces"]; ok {
		if err := json.Unmarshal(w, &workspaces); err != nil {
			return fmt.Errorf("decode trustedWorkspaces in %s: %w", path, err)
		}
	}
	if slices.Contains(workspaces, dir) {
		return nil
	}
	workspaces = append(workspaces, dir)
	if cfg["trustedWorkspaces"], err = json.Marshal(workspaces); err != nil {
		return err
	}
	return writeConfig(path, cfg, mode, ".settings.json.peyk-*")
}

func readConfig(path string) (map[string]json.RawMessage, fs.FileMode, error) {
	cfg := map[string]json.RawMessage{}
	mode := fs.FileMode(0o600)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, 0, err
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, 0, fmt.Errorf("decode %s: %w", path, err)
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	return cfg, mode, nil
}

func writeConfig(path string, cfg map[string]json.RawMessage, mode fs.FileMode, pattern string) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// The agent CLI rewrites its settings file while running, so replace the
	// file in one rename rather than through a partial write.
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
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
