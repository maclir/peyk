package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/session"
)

func TestSwapStartsFreshAndStopsPreviousSession(t *testing.T) {
	var calls []string
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		if len(args) >= 4 && args[2] == "session" && args[3] == "show" {
			return []byte(`{"id":"new-id","title":"knip","tool":"antigravity","state":"live"}`), nil
		}
		return nil, nil
	}
	a := actions{aoe: laptop.Aoe{Profile: "personal", Run: run}, group: "personal", claudeConfig: filepath.Join(t.TempDir(), ".claude.json")}
	s := session.Snapshot{ID: "old123456789", Title: "knip", Path: "/c/knip", AoeStatus: "idle", Transcript: session.Transcript{ID: "conv-1"}}

	id, err := a.Swap(context.Background(), s, session.Antigravity, "gemini-4-neon-high")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new-id" {
		t.Fatalf("id = %q, want new-id", id)
	}
	want := []string{
		"aoe -p personal session rename old123456789 -t knip~old12345",
		"aoe -p personal add /c/knip -t knip -g personal --tool antigravity --extra-args --model gemini-4-neon-high",
		"aoe -p personal session show knip --json",
		"aoe -p personal session stop old123456789",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestSwapToClaudeTrustsFolderAndSkipsStopWhenAlreadyStopped(t *testing.T) {
	var calls []string
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) >= 4 && args[2] == "session" && args[3] == "show" {
			return []byte(`{"id":"new-cc","title":"gemi","tool":"claude","state":"live"}`), nil
		}
		return nil, nil
	}
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	a := actions{aoe: laptop.Aoe{Profile: "personal", Run: run}, group: "personal", claudeConfig: cfg}
	s := session.Snapshot{ID: "old123456789", Title: "gemi", Path: "/c/gemi", Tool: session.Antigravity, AoeStatus: "stopped"}

	id, err := a.Swap(context.Background(), s, "", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new-cc" {
		t.Fatalf("id = %q, want new-cc", id)
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if projects, _ := parsed["projects"].(map[string]any); projects["/c/gemi"] == nil {
		t.Fatalf("claude config did not trust /c/gemi: %s", b)
	}
	for _, c := range calls {
		if strings.Contains(c, "session stop") || strings.Contains(c, "set-session-id") {
			t.Fatalf("unexpected call %q in %v", c, calls)
		}
	}
}

func TestSwapRestoresOldTitleWhenAddFails(t *testing.T) {
	var calls []string
	run := func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) >= 3 && args[2] == "add" {
			return nil, errors.New("boom")
		}
		return nil, nil
	}
	a := actions{aoe: laptop.Aoe{Profile: "personal", Run: run}, group: "personal", claudeConfig: filepath.Join(t.TempDir(), ".claude.json")}
	s := session.Snapshot{ID: "old123456789", Title: "knip", Path: "/c/knip", AoeStatus: "idle"}

	if _, err := a.Swap(context.Background(), s, session.Antigravity, "gemini-4-neon-high"); err == nil {
		t.Fatal("want error")
	}
	want := []string{
		"aoe -p personal session rename old123456789 -t knip~old12345",
		"aoe -p personal add /c/knip -t knip -g personal --tool antigravity --extra-args --model gemini-4-neon-high",
		"aoe -p personal session rename old123456789 -t knip",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}
