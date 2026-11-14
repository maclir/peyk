package analytics

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// DayLayout and HourLayout are how a day and an hour are written. Both are UTC
// in both readers.
const (
	DayLayout  = "2006-01-02"
	HourLayout = "2006-01-02T15"
)

// Count is how many times one event happened in one hour with one combination
// of dimension values, and the totals of its summed fields over those events.
// Dims holds only non-empty values; Sums holds every summed field of the event.
type Count struct {
	Hour  string
	Event string
	Dims  map[string]string
	N     int
	Sums  map[string]int
}

// Day is the UTC day c falls on.
func (c Count) Day() string { return c.Hour[:len(DayLayout)] }

// Snapshot is everything the warehouse holds from Since onwards.
type Snapshot struct {
	Since  time.Time
	Counts []Count
	// Latest is when the newest counted event in the window was written, zero
	// when there is none. The dashboard shows it so that a pipeline which has
	// stopped delivering looks stale instead of looking quiet.
	Latest time.Time
}

// Reader answers the dashboard. BigQuery in production, Memory everywhere else;
// both return the same counts for the same lines.
type Reader interface {
	Read(ctx context.Context, since time.Time) (Snapshot, error)
}

// Match narrows a sum to rows whose dimension has a value.
type Match struct{ Dim, Value string }

// Is matches rows where dim equals value. A boolean dimension is "true" or
// "false".
func Is(dim, value string) Match { return Match{Dim: dim, Value: value} }

// Total sums e over the window.
func (s Snapshot) Total(e Event, match ...Match) int {
	n := 0
	for _, c := range s.rows(e, "", match) {
		n += c.N
	}
	return n
}

// PerDay sums e per day. Days with no events are absent; see Days.
func (s Snapshot) PerDay(e Event, match ...Match) map[string]int {
	out := map[string]int{}
	for _, c := range s.rows(e, "", match) {
		out[c.Day()] += c.N
	}
	return out
}

// PerHour sums e per hour. Hours with no events are absent; see Hours.
func (s Snapshot) PerHour(e Event, match ...Match) map[string]int {
	out := map[string]int{}
	for _, c := range s.rows(e, "", match) {
		out[c.Hour] += c.N
	}
	return out
}

// SumTotal totals e's summed field over the window.
func (s Snapshot) SumTotal(e Event, field string, match ...Match) int {
	mustSum(e, field)
	n := 0
	for _, c := range s.rows(e, "", match) {
		n += c.Sums[field]
	}
	return n
}

// SumPerDay totals e's summed field per day.
func (s Snapshot) SumPerDay(e Event, field string, match ...Match) map[string]int {
	mustSum(e, field)
	out := map[string]int{}
	for _, c := range s.rows(e, "", match) {
		out[c.Day()] += c.Sums[field]
	}
	return out
}

// SumBy totals e's summed field per value of dim.
func (s Snapshot) SumBy(e Event, field, dim string, match ...Match) map[string]int {
	mustSum(e, field)
	out := map[string]int{}
	for _, c := range s.rows(e, dim, match) {
		out[c.Dims[dim]] += c.Sums[field]
	}
	return out
}

// mustSum panics on a field e does not total, for the reason rows panics on an
// undeclared dimension.
func mustSum(e Event, field string) {
	if !slices.Contains(Sums(e), field) {
		panic(fmt.Sprintf("analytics: %s does not sum %q", e.EventName(), field))
	}
}

// By sums e per value of dim. Rows without the dimension sum under "".
func (s Snapshot) By(e Event, dim string, match ...Match) map[string]int {
	out := map[string]int{}
	for _, c := range s.rows(e, dim, match) {
		out[c.Dims[dim]] += c.N
	}
	return out
}

// rows selects e's counts. It panics on a sensitive event or a dimension e does
// not declare, because either would return zero forever instead of failing:
// the dashboard's tests exercise every sum, so the panic surfaces there.
func (s Snapshot) rows(e Event, by string, match []Match) []Count {
	if IsSensitive(e) {
		panic(fmt.Sprintf("analytics: %s is sensitive and never counted", e.EventName()))
	}
	dims := Dims(e)
	for _, d := range append([]string{by}, dimsOf(match)...) {
		if d != "" && !slices.Contains(dims, d) {
			panic(fmt.Sprintf("analytics: %s has no dimension %q", e.EventName(), d))
		}
	}
	var out []Count
	for _, c := range s.Counts {
		if c.Event != e.EventName() {
			continue
		}
		ok := true
		for _, m := range match {
			if c.Dims[m.Dim] != m.Value {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

func dimsOf(match []Match) []string {
	out := make([]string, len(match))
	for i, m := range match {
		out[i] = m.Dim
	}
	return out
}

// Range is a window the dashboard offers. Key is what the query string carries.
type Range struct {
	Key    string
	Label  string
	Window time.Duration
}

// Ranges are the windows offered, in display order.
var Ranges = []Range{
	{Key: "24h", Label: "24 hours", Window: 24 * time.Hour},
	{Key: "7d", Label: "7 days", Window: 7 * 24 * time.Hour},
	{Key: "30d", Label: "30 days", Window: 30 * 24 * time.Hour},
}

// DefaultRange is what an unspecified or unknown key shows.
var DefaultRange = Ranges[1]

// ParseRange reads a range key, falling back to DefaultRange.
func ParseRange(key string) Range {
	for _, r := range Ranges {
		if r.Key == key {
			return r
		}
	}
	return DefaultRange
}

// Days lists every UTC day from since to now inclusive, so a chart can draw a
// day with no events as zero instead of leaving it out.
func Days(since, now time.Time) []string { return steps(since, now, 24*time.Hour, DayLayout) }

// Hours lists every UTC hour from since to now inclusive, as Days does.
func Hours(since, now time.Time) []string { return steps(since, now, time.Hour, HourLayout) }

func steps(since, now time.Time, step time.Duration, layout string) []string {
	var out []string
	for d := since.UTC().Truncate(step); !d.After(now.UTC()); d = d.Add(step) {
		out = append(out, d.Format(layout))
	}
	return out
}
