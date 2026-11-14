package analytics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory stands in for both sinks and the warehouse. It takes the lines Emit
// writes and answers with the counts and raw rows the BigQuery queries would
// return for them, so the local server and the end-to-end suite run the real
// dashboard, and the tests catch an emitter and a reader that disagree.
type Memory struct {
	mu        sync.Mutex
	now       func() time.Time
	rows      []memoryRow
	sensitive []memoryRaw
	err       error
}

type memoryRow struct {
	at    time.Time
	event string
	dims  map[string]string
	sums  map[string]int
}

type memoryRaw struct {
	at      time.Time
	event   string
	payload json.RawMessage
}

// NewMemory builds an empty warehouse. The clock stamps each line as it
// arrives, which is what Cloud Logging does.
func NewMemory(now func() time.Time) *Memory { return &Memory{now: now} }

// Fail makes every read return err, so the dashboard's unavailable state is
// testable.
func (m *Memory) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Write accepts log lines. Each line goes where its sink filter would send it,
// and a line neither filter selects is dropped.
func (m *Memory) Write(p []byte) (int, error) {
	sc := bufio.NewScanner(bytes.NewReader(p))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		m.accept(sc.Bytes())
	}
	return len(p), nil
}

func (m *Memory) accept(line []byte) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return
	}
	// Both filters: the marker is present, and sensitive says which sink.
	if marker, ok := fields[MarkerKey].(json.Number); !ok || marker.String() == "0" {
		return
	}
	sensitive, ok := fields[SensitiveKey].(bool)
	if !ok {
		return
	}
	name, _ := fields[EventKey].(string)
	at := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	if sensitive {
		m.sensitive = append(m.sensitive, memoryRaw{at: at, event: name, payload: append(json.RawMessage(nil), line...)})
		return
	}
	// The counting query's own filter: only catalogued events.
	e, ok := counted()[name]
	if !ok {
		return
	}
	sums := map[string]int{}
	for _, f := range Sums(e) {
		if n, ok := fields[f].(json.Number); ok {
			v, _ := n.Int64()
			sums[f] = int(v)
		} else {
			sums[f] = 0
		}
	}
	dims := map[string]string{}
	for _, d := range allDims() {
		if v := jsonValue(fields[d]); v != "" {
			dims[d] = v
		}
	}
	m.rows = append(m.rows, memoryRow{at: at, event: name, dims: dims, sums: sums})
}

// jsonValue renders a value the way BigQuery's JSON_VALUE does for the scalar
// kinds a dimension may have.
func jsonValue(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// Read groups the lines from since onwards the way the query groups rows.
func (m *Memory) Read(_ context.Context, since time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return Snapshot{}, m.err
	}
	snap := Snapshot{Since: since}
	index := map[string]int{}
	for _, r := range m.rows {
		if r.at.Before(since) {
			continue
		}
		if r.at.After(snap.Latest) {
			snap.Latest = r.at
		}
		hour := r.at.Format(HourLayout)
		key := groupKey(hour, r.event, r.dims)
		i, ok := index[key]
		if !ok {
			i = len(snap.Counts)
			index[key] = i
			snap.Counts = append(snap.Counts, Count{Hour: hour, Event: r.event, Dims: r.dims, Sums: zeroSums(r.event)})
		}
		snap.Counts[i].N++
		for f, v := range r.sums {
			snap.Counts[i].Sums[f] += v
		}
	}
	sortCounts(snap.Counts)
	return snap, nil
}

// ReadRaw returns e's lines from since onwards, newest first, at most limit.
func (m *Memory) ReadRaw(_ context.Context, e Event, since time.Time, limit int) ([]Raw, error) {
	mustBeSensitive(e)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var out []Raw
	for i := len(m.sensitive) - 1; i >= 0 && len(out) < limit; i-- {
		r := m.sensitive[i]
		if r.event == e.EventName() && !r.at.Before(since) {
			out = append(out, Raw{At: r.at, Payload: r.payload})
		}
	}
	return out, nil
}

func groupKey(hour, event string, dims map[string]string) string {
	var b strings.Builder
	b.WriteString(hour + "\x00" + event)
	for _, d := range allDims() {
		b.WriteString("\x00" + dims[d])
	}
	return b.String()
}

// sortCounts orders counts deterministically so both readers return the same
// slice for the same rows.
func sortCounts(cs []Count) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		if a.Event != b.Event {
			return a.Event < b.Event
		}
		return groupKey("", "", a.Dims) < groupKey("", "", b.Dims)
	})
}

// zeroSums starts a count with every summed field of its event at zero, which
// is what the query returns for an event that carried none.
func zeroSums(event string) map[string]int {
	out := map[string]int{}
	if e, ok := counted()[event]; ok {
		for _, f := range Sums(e) {
			out[f] = 0
		}
	}
	return out
}
