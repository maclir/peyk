package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
