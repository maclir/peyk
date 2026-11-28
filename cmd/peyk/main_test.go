package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/launchd"
	"github.com/maclir/peyk/internal/session"
)

func TestPrintBoard(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printBoard(&out, []session.Snapshot{
		{Title: "knip", State: session.Unpushed, Git: &session.Git{
			Upstream: "origin/main", Unpushed: 2,
			Last: &session.Commit{Hash: "3f2a1c4", Subject: "Report unused barrel exports", At: now.Add(-12 * time.Minute)},
		}, Message: &session.Message{Text: "Committed.\n\n`make test` passes."}},
		{Title: "scratch", State: session.Idle, Git: &session.Git{Unpushed: 1, Dirty: 3}},
		{Title: "notes", State: session.Idle},
	}, now)
	want := []string{
		"knip           unpushed     3f2a1c4 Report unused barrel exports (12m) · 2 unpushed",
		"               Committed. `make test` passes.",
		"scratch        idle         1 unpushed, no remote branch · 3 changed files",
		"notes          idle         no repository",
	}
	if got := strings.TrimRight(out.String(), "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestPrintBoardEmpty(t *testing.T) {
	var out bytes.Buffer
	printBoard(&out, nil, time.Now())
	if !strings.Contains(out.String(), "No live") {
		t.Fatal(out.String())
	}
}

func TestOneLineTruncates(t *testing.T) {
	if got := oneLine("a  b\nc", 10); got != "a b c" {
		t.Fatal(got)
	}
	if got := oneLine("abcdefghij", 5); got != "abcd…" {
		t.Fatal(got)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(context.Background(), []string{"nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("want error")
	}
	var out bytes.Buffer
	if err := run(context.Background(), nil, &out); err != nil || !strings.Contains(out.String(), "status") {
		t.Fatalf("usage: %v %q", err, out.String())
	}
}

func TestBillToNamesTheProject(t *testing.T) {
	var got string
	rt := billTo{project: "p", next: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Get("X-Goog-User-Project")
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if _, err := rt.RoundTrip(req); err != nil || got != "p" {
		t.Fatalf("header = %q, %v", got, err)
	}
	if req.Header.Get("X-Goog-User-Project") != "" {
		t.Fatal("the caller's request was modified")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJobsRunTheAgentThroughMakeAndGiveTheMenuTheOrigin(t *testing.T) {
	env := map[string]string{"PATH": "/bin", "PEYK_ORIGIN": "https://peyk.example", "GOOGLE_CLOUD_PROJECT": "p"}
	js := jobs("/code/peyk", "/h", func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	agent, menu := js[0], js[1]
	if agent.Label != launchd.Agent || strings.Join(agent.Program, " ") != "/usr/bin/make -s serve" || agent.Dir != "/code/peyk" || !agent.KeepAlive {
		t.Fatalf("agent job = %+v", agent)
	}
	if len(agent.Env) != 1 || agent.Env["PATH"] != "/bin" {
		t.Fatalf("agent env = %v; the project's settings come from .env", agent.Env)
	}
	if menu.Label != launchd.Menu || menu.Program[0] != "/code/peyk/bin/Peyk.app/Contents/MacOS/Peyk" || menu.KeepAlive {
		t.Fatalf("menu job = %+v", menu)
	}
	if menu.Env["PEYK_ORIGIN"] != "https://peyk.example" || menu.Env["PEYK_REPO"] != "/code/peyk" || len(menu.Env) != 2 {
		t.Fatalf("menu env = %v", menu.Env)
	}
}

func TestParsePublishArgs(t *testing.T) {
	p, title, err := parsePublishArgs([]string{"/tmp/demo.html"})
	if err != nil || p != "/tmp/demo.html" || title != "" {
		t.Fatalf("got %q %q %v", p, title, err)
	}
	p, title, err = parsePublishArgs([]string{"/tmp/demo.html", "--title", "Working tag"})
	if err != nil || p != "/tmp/demo.html" || title != "Working tag" {
		t.Fatalf("got %q %q %v", p, title, err)
	}
	p, title, err = parsePublishArgs([]string{"--title=Demo", "/tmp/demo.html"})
	if err != nil || p != "/tmp/demo.html" || title != "Demo" {
		t.Fatalf("got %q %q %v", p, title, err)
	}
	for _, bad := range [][]string{nil, {"--title"}, {"--nope", "a.html"}, {"a.html", "b.html"}} {
		if _, _, err := parsePublishArgs(bad); err == nil {
			t.Errorf("parsePublishArgs(%v) succeeded, want error", bad)
		}
	}
}

func TestPublishPrototypeSendsFileOverSocket(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "working-tag.html")
	body := "<!doctype html><title>Motion Demo</title><p>hi</p>"
	if err := os.WriteFile(htmlPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("/tmp", "peyk-pub-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "p.sock")
	ln, err := agent.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan agent.PublishCall, 1)
	go agent.ServePublish(ctx, ln, calls)
	go func() {
		call := <-calls
		if call.Req.Dir != "/code/peyk" || call.Req.Title != "Motion Demo" || call.Req.HTML != body {
			call.Reply <- agent.PublishResponse{Error: "unexpected request"}
			return
		}
		call.Reply <- agent.PublishResponse{ID: "abc", URL: "https://peyk.test/#p-abc"}
	}()
	var out bytes.Buffer
	if err := publishPrototype(ctx, []string{htmlPath}, sock, "/code/peyk", &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "https://peyk.test/#p-abc" {
		t.Fatalf("out = %q", out.String())
	}
}
