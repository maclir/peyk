package laptop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestGitHubActionsCountsTheMonthsComputeInLinuxMinutes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = io.WriteString(w, `{"login":"maclir"}`)
		case "/users/maclir/settings/billing/usage":
			if q := r.URL.Query(); q.Get("year") != "2026" || q.Get("month") != "11" {
				t.Errorf("asked for %s", r.URL)
			}
			_, _ = io.WriteString(w, `{"usageItems":[
				{"product":"actions","sku":"actions_linux","grossAmount":1.2,"repositoryName":"maclir/a"},
				{"product":"actions","sku":"actions_linux","grossAmount":0.6,"repositoryName":"maclir/b"},
				{"product":"actions","sku":"actions_macos","grossAmount":0.62,"repositoryName":"maclir/b"},
				{"product":"actions","sku":"actions_linux","grossAmount":0.06,"repositoryName":"maclir/c"},
				{"product":"actions","sku":"actions_linux","grossAmount":0.012,"repositoryName":"maclir/d"},
				{"product":"actions","sku":"actions_storage","grossAmount":5,"repositoryName":"maclir/a"},
				{"product":"copilot","sku":"copilot_premium_request","grossAmount":3}
			]}`)
		default:
			t.Errorf("asked for %s", r.URL)
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 17, 23, 30, 0, 0, time.FixedZone("CET", 3600))
	got, err := GitHub{Client: srv.Client(), BaseURL: srv.URL}.Actions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	want := session.Actions{
		Included: 2000, Used: 415, ResetsAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Repos: []session.RepoUsage{{Repo: "maclir/b", Minutes: 203}, {Repo: "maclir/a", Minutes: 200}, {Repo: "maclir/c", Minutes: 10}},
	}
	if got.Included != want.Included || got.Used != want.Used || !got.ResetsAt.Equal(want.ResetsAt) || fmt.Sprint(got.Repos) != fmt.Sprint(want.Repos) {
		t.Fatalf("got %+v, want %+v", *got, want)
	}
}

func TestGitHubActionsReportsMissingBillingAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = io.WriteString(w, `{"login":"maclir"}`)
			return
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := (GitHub{Client: srv.Client(), BaseURL: srv.URL}).Actions(context.Background(), time.Now()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
