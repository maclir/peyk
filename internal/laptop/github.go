package laptop

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

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
	var body struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	path := fmt.Sprintf("/repos/%s/actions/runs?head_sha=%s&per_page=30", repo, url.QueryEscape(sha))
	if err := g.get(ctx, path, &body); err != nil {
		return nil, fmt.Errorf("workflow runs of %s: %w", repo, err)
	}
	return summarise(sha, body.Runs), nil
}

// GitHub Free includes this much Actions compute a month, and a standard
// Linux runner minute costs linuxMinuteUSD of it.
const (
	includedActionsUSD = 12.0
	linuxMinuteUSD     = 0.006
	// actionsRepos is how many repositories the phone lists by usage.
	actionsRepos = 3
)

// Actions reports the account's Actions usage in the month of now. The
// billing API needs gh's "user" scope; without it GitHub answers 404.
func (g GitHub) Actions(ctx context.Context, now time.Time) (*session.Actions, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := g.get(ctx, "/user", &user); err != nil {
		return nil, fmt.Errorf("github user: %w", err)
	}
	now = now.UTC()
	var body struct {
		Items []struct {
			Product     string  `json:"product"`
			SKU         string  `json:"sku"`
			GrossAmount float64 `json:"grossAmount"`
			Repo        string  `json:"repositoryName"`
		} `json:"usageItems"`
	}
	path := fmt.Sprintf("/users/%s/settings/billing/usage?year=%d&month=%d", url.PathEscape(user.Login), now.Year(), int(now.Month()))
	if err := g.get(ctx, path, &body); err != nil {
		return nil, fmt.Errorf("actions billing of %s: %w", user.Login, err)
	}
	var total float64
	byRepo := map[string]float64{}
	for _, it := range body.Items {
		// Artifact and cache storage has an allowance of its own.
		if !strings.EqualFold(it.Product, "actions") || strings.Contains(strings.ToLower(it.SKU), "storage") {
			continue
		}
		total += it.GrossAmount
		if it.Repo != "" {
			byRepo[it.Repo] += it.GrossAmount
		}
	}
	minutes := func(usd float64) int { return int(math.Round(usd / linuxMinuteUSD)) }
	a := &session.Actions{
		Included: minutes(includedActionsUSD),
		Used:     minutes(total),
		ResetsAt: time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC),
		Repos:    []session.RepoUsage{},
	}
	for repo, usd := range byRepo {
		if m := minutes(usd); m > 0 {
			a.Repos = append(a.Repos, session.RepoUsage{Repo: repo, Minutes: m})
		}
	}
	sort.Slice(a.Repos, func(i, j int) bool {
		if a.Repos[i].Minutes != a.Repos[j].Minutes {
			return a.Repos[i].Minutes > a.Repos[j].Minutes
		}
		return a.Repos[i].Repo < a.Repos[j].Repo
	})
	if len(a.Repos) > actionsRepos {
		a.Repos = a.Repos[:actionsRepos]
	}
	return a, nil
}

func (g GitHub) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(g.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
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
