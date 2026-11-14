package laptop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/maclir/peyk/internal/session"
)

// GitHub reads workflow runs from the GitHub REST API.
type GitHub struct {
	Client *http.Client
	// BaseURL is https://api.github.com outside tests.
	BaseURL string
	Token   string
}

type workflowRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

// Runs summarises the workflow runs for one commit of repo (owner/name). It
// returns nil while GitHub has none, which is also the state just after a
// push, before a run is queued.
func (g GitHub) Runs(ctx context.Context, repo, sha string) (*session.CI, error) {
	u := fmt.Sprintf("%s/repos/%s/actions/runs?head_sha=%s&per_page=30", strings.TrimSuffix(g.BaseURL, "/"), repo, url.QueryEscape(sha))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workflow runs of %s: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workflow runs of %s: %s", repo, resp.Status)
	}
	var body struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("workflow runs of %s: %w", repo, err)
	}
	return summarise(sha, body.Runs), nil
}

func summarise(sha string, runs []workflowRun) *session.CI {
	if len(runs) == 0 {
		return nil
	}
	pick := -1
	for _, want := range []session.CIState{session.CIFailure, session.CIRunning, session.CIQueued, session.CISuccess} {
		for i, r := range runs {
			if runState(r) == want {
				pick = i
				break
			}
		}
		if pick >= 0 {
			break
		}
	}
	if pick < 0 {
		pick = 0
	}
	r := runs[pick]
	return &session.CI{SHA: sha, Workflow: r.Name, State: runState(r), URL: r.HTMLURL}
}

func runState(r workflowRun) session.CIState {
	switch r.Status {
	case "completed":
	case "in_progress":
		return session.CIRunning
	default:
		return session.CIQueued
	}
	switch r.Conclusion {
	case "success", "neutral", "skipped":
		return session.CISuccess
	case "cancelled":
		return session.CICancelled
	default:
		return session.CIFailure
	}
}
