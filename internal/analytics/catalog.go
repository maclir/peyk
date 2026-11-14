// Package analytics writes the events the admin dashboard counts and reads the
// counts back.
//
// Events are JSON lines on stdout. Cloud Run turns each line into a log entry,
// a log sink copies the entries into BigQuery, and the dashboard queries the
// sink's table. Every file in this package except events.go is shared verbatim
// between projects; events.go is the project's own catalog.
//
// The dashboard reports only what these events say happened. It never reads
// application state, because a document says what is true now and a dashboard
// figure has to say what happened in a window.
package analytics

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Event is one thing worth counting.
//
// The name becomes a value in a BigQuery column, so renaming an event leaves
// every figure built on the old name at zero. testdata/events.golden pins every
// name and field for that reason.
type Event interface {
	EventName() string
}

// Sensitive marks an event that carries data about a person. Embed it in the
// event struct.
//
// A sensitive event goes to its own dataset through its own sink and is never
// read by the counting query, so purging it means deleting that sink and
// dataset without touching the counts.
type Sensitive struct{}

func (Sensitive) sensitive() {}

type sensitiveEvent interface{ sensitive() }

// IsSensitive reports whether e embeds Sensitive.
func IsSensitive(e Event) bool {
	_, ok := e.(sensitiveEvent)
	return ok
}

// dimTag marks a field the dashboard may group by: `analytics:"dim"`.
//
// Only strings and booleans can be dimensions. A number reaches BigQuery
// through Cloud Logging as a double, so an integer dimension would read back as
// "3.0" in the warehouse and "3" in memory, and the two readers would disagree.
const dimTag = "dim"

// sumTag marks an integer field the dashboard may total: `analytics:"sum"`.
// A count says how often something happened; a sum says how much, such as how
// many items one addition produced.
const sumTag = "sum"

// Field is one field of an event as it appears on the log line.
type Field struct {
	Name string
	Kind reflect.Kind
	Dim  bool
	Sum  bool
}

// Fields lists e's fields in declaration order.
func Fields(e Event) []Field {
	t := reflect.TypeOf(e)
	var out []Field
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type == reflect.TypeFor[Sensitive]() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			panic(fmt.Sprintf("analytics: %s.%s needs a json name", t.Name(), f.Name))
		}
		tag := f.Tag.Get("analytics")
		out = append(out, Field{Name: name, Kind: f.Type.Kind(), Dim: tag == dimTag, Sum: tag == sumTag})
	}
	return out
}

// Dims lists the dimensions e declares.
func Dims(e Event) []string {
	var out []string
	for _, f := range Fields(e) {
		if f.Dim {
			out = append(out, f.Name)
		}
	}
	return out
}

// Sums lists the fields e totals.
func Sums(e Event) []string {
	var out []string
	for _, f := range Fields(e) {
		if f.Sum {
			out = append(out, f.Name)
		}
	}
	return out
}

// counted is every event the counting query reads, by name.
func counted() map[string]Event {
	out := map[string]Event{}
	for _, e := range All() {
		if !IsSensitive(e) {
			out[e.EventName()] = e
		}
	}
	return out
}

// countedNames is counted's names, sorted, which is the order the query binds
// them in.
func countedNames() []string {
	var out []string
	for name := range counted() {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// allDims is the union of every counted event's dimensions, sorted. The query
// selects each one for every row, and an event that lacks one reads it as "".
func allDims() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range counted() {
		for _, d := range Dims(e) {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// allSums is the union of every counted event's summed fields, sorted.
func allSums() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range counted() {
		for _, f := range Sums(e) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}
