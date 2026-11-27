package session

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Usage is how much of each subscription's rate limits is used, as the
// coding tools last reported it. The limits belong to the account, so every
// session reports the same windows. FiveHour and SevenDay are Claude Code's;
// Antigravity keeps one pool of limits per group of models.
type Usage struct {
	FiveHour    *Window `firestore:"five_hour,omitempty" json:"five_hour,omitempty"`
	SevenDay    *Window `firestore:"seven_day,omitempty" json:"seven_day,omitempty"`
	Antigravity []Pool  `firestore:"antigravity,omitempty" json:"antigravity,omitempty"`
}

// Pool is one Antigravity quota, shared by a group of models such as Gemini.
type Pool struct {
	Name     string  `firestore:"name" json:"name"`
	FiveHour *Window `firestore:"five_hour,omitempty" json:"five_hour,omitempty"`
	SevenDay *Window `firestore:"seven_day,omitempty" json:"seven_day,omitempty"`
}

// Window is one rate-limit window. Used is a percentage, 0 to 100.
type Window struct {
	Used     float64   `firestore:"used" json:"used"`
	ResetsAt time.Time `firestore:"resets_at" json:"resets_at"`
}

// ParseStatusLine reads the JSON Claude Code gives a status line command. It
// returns nil when the input has no rate limits: they are reported only to
// subscribers, and only after a session's first response.
func ParseStatusLine(r io.Reader) (*Usage, error) {
	type window struct {
		UsedPercentage float64 `json:"used_percentage"`
		ResetsAt       int64   `json:"resets_at"`
	}
	var in struct {
		RateLimits *struct {
			FiveHour *window `json:"five_hour"`
			SevenDay *window `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return nil, fmt.Errorf("read status line input: %w", err)
	}
	rl := in.RateLimits
	if rl == nil || rl.FiveHour == nil && rl.SevenDay == nil {
		return nil, nil
	}
	conv := func(w *window) *Window {
		if w == nil {
			return nil
		}
		return &Window{Used: w.UsedPercentage, ResetsAt: time.Unix(w.ResetsAt, 0).UTC()}
	}
	return &Usage{FiveHour: conv(rl.FiveHour), SevenDay: conv(rl.SevenDay)}, nil
}

// ParseQuota reads the quota buckets Antigravity gives its status line
// command, keyed like "gemini-weekly": a model group, then the window. It
// returns nil when the input carries no quota, as Claude Code's does not.
// A bucket reported only as an amount, or disabled, has no percentage to
// show and is left out.
func ParseQuota(b []byte) ([]Pool, error) {
	type bucket struct {
		RemainingFraction *float64  `json:"remaining_fraction"`
		ResetTime         time.Time `json:"reset_time"`
		Disabled          bool      `json:"disabled"`
	}
	var in struct {
		Quota map[string]bucket `json:"quota"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("read status line input: %w", err)
	}
	byGroup := map[string]*Pool{}
	var order []string
	for id, q := range in.Quota {
		if q.Disabled || q.RemainingFraction == nil {
			continue
		}
		group, window, ok := strings.Cut(id, "-")
		if !ok {
			continue
		}
		w := &Window{Used: 100 * (1 - *q.RemainingFraction), ResetsAt: q.ResetTime.UTC()}
		p := byGroup[group]
		if p == nil {
			p = &Pool{Name: poolName(group)}
			byGroup[group] = p
			order = append(order, group)
		}
		switch {
		case strings.Contains(window, "week"):
			p.SevenDay = w
		case strings.Contains(window, "5h") || strings.Contains(window, "hour"):
			p.FiveHour = w
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	// Gemini first, as Antigravity's own quota panel lists it.
	sort.Slice(order, func(i, j int) bool {
		if (order[i] == "gemini") != (order[j] == "gemini") {
			return order[i] == "gemini"
		}
		return order[i] < order[j]
	})
	out := make([]Pool, 0, len(order))
	for _, g := range order {
		out = append(out, *byGroup[g])
	}
	return out, nil
}

// poolName names a bucket group as peyk shows it. Antigravity calls the pool
// it shares between Claude and GPT "3p", for third-party models.
func poolName(group string) string {
	if group == "3p" {
		return "Claude"
	}
	if group == "" {
		return group
	}
	return strings.ToUpper(group[:1]) + group[1:]
}

// Actions is how much of the month's included GitHub Actions compute the
// account has used, in standard Linux runner minutes: GitHub charges other
// runners more per minute from the same allowance, so a minute on them
// counts as several here.
type Actions struct {
	Included int         `firestore:"included" json:"included"`
	Used     int         `firestore:"used" json:"used"`
	ResetsAt time.Time   `firestore:"resets_at" json:"resets_at"`
	Repos    []RepoUsage `firestore:"repos" json:"repos"`
}

// RepoUsage is one repository's share of Actions.Used.
type RepoUsage struct {
	Repo    string `firestore:"repo" json:"repo"`
	Minutes int    `firestore:"minutes" json:"minutes"`
}
