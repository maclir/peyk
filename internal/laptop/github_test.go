package laptop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/session"
)

func TestGitHubRunsAsksForTheCommitAndPicksTheRunThatMatters(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/maclir/knip/actions/runs" || r.URL.Query().Get("head_sha") != "abc123" {
			t.Errorf("asked for %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	gh := GitHub{Client: srv.Client(), BaseURL: srv.URL, Token: "tok"}
	run := func(name, status, conclusion string) string {
		return `{"name":"` + name + `","status":"` + status + `","conclusion":"` + conclusion + `","html_url":"https://github.com/maclir/knip/actions/runs/` + name + `"}`
	}
	for _, c := range []struct {
		runs []string
		want *session.CI
	}{
		{nil, nil},
		{[]string{run("lint", "completed", "success"), run("deploy", "in_progress", "")},
			&session.CI{SHA: "abc123", Workflow: "deploy", State: session.CIRunning, URL: "https://github.com/maclir/knip/actions/runs/deploy"}},
		{[]string{run("lint", "queued", ""), run("deploy", "completed", "timed_out")},
			&session.CI{SHA: "abc123", Workflow: "deploy", State: session.CIFailure, URL: "https://github.com/maclir/knip/actions/runs/deploy"}},
		{[]string{run("old", "completed", "cancelled"), run("deploy", "completed", "success")},
			&session.CI{SHA: "abc123", Workflow: "deploy", State: session.CISuccess, URL: "https://github.com/maclir/knip/actions/runs/deploy"}},
	} {
		body = `{"workflow_runs":[` + strings.Join(c.runs, ",") + `]}`
		got, err := gh.Runs(context.Background(), "maclir/knip", "abc123")
		if err != nil {
			t.Fatal(err)
		}
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("runs %v: got %+v, want %+v", c.runs, got, c.want)
		}
	}
}

func TestGitHubRunsReportsAFailedRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := (GitHub{Client: srv.Client(), BaseURL: srv.URL}).Runs(context.Background(), "maclir/gone", "abc"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
