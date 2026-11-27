package laptop

import (
	"path/filepath"
	"testing"

	"github.com/maclir/peyk/internal/session"
)

func TestConversationsRememberEachToolsLastConversationPerDirectory(t *testing.T) {
	c := Conversations{Path: filepath.Join(t.TempDir(), "peyk", "conversations.json")}
	if id, err := c.Get("/c/knip", ""); err != nil || id != "" {
		t.Fatalf("Get before any Set = %q, %v", id, err)
	}
	if err := c.Set("/c/knip", "", "claude-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("/c/knip", session.Antigravity, "agy-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("/c/knip", session.Antigravity, ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := c.Get("/c/knip", "claude"); id != "claude-1" {
		t.Fatalf("Claude Code = %q", id)
	}
	if id, _ := c.Get("/c/knip", session.Antigravity); id != "agy-1" {
		t.Fatalf("Antigravity = %q, want an empty Set to keep it", id)
	}
	if id, _ := c.Get("/c/other", ""); id != "" {
		t.Fatalf("another directory = %q", id)
	}
}
