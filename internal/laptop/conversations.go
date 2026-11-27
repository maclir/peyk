package laptop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Conversations remembers, per working directory, the conversation each tool
// last had there, so a worker moved back to a tool picks up where that tool
// left off. aoe forgets it: a session runs one tool, and moving the worker
// replaces its session.
type Conversations struct{ Path string }

type conversationFile map[string]map[string]string

func (c Conversations) load() (conversationFile, error) {
	b, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return conversationFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	f := conversationFile{}
	return f, json.Unmarshal(b, &f)
}

// Get is the conversation tool last had in dir, or "".
func (c Conversations) Get(dir, tool string) (string, error) {
	f, err := c.load()
	if err != nil {
		return "", err
	}
	return f[dir][toolKey(tool)], nil
}

// Set remembers conversation as the one tool has in dir.
func (c Conversations) Set(dir, tool, conversation string) error {
	if conversation == "" {
		return nil
	}
	f, err := c.load()
	if err != nil {
		return err
	}
	if f[dir] == nil {
		f[dir] = map[string]string{}
	}
	f[dir][toolKey(tool)] = conversation
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

// toolKey names Claude Code, whose tool is "", the way aoe does.
func toolKey(tool string) string {
	if tool == "" {
		return "claude"
	}
	return tool
}
