package session

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Usage is how much of the Claude subscription's rate limits is used, as
// Claude Code last reported it. The limits belong to the account, so every
// session reports the same windows.
type Usage struct {
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
