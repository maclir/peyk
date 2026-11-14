package analytics

import (
	"encoding/json"
	"io"
	"log"
	"sync"
)

// Keys every line carries besides the event's own fields. An event field may
// not reuse one; the schema test enforces it.
//
// severity and message are the two keys Cloud Logging lifts out of a JSON
// line. The sink filters select on MarkerKey and SensitiveKey, so both are
// written on every line rather than only when true: a filter that tests for a
// missing key is a filter that also matches every other log line.
const (
	MarkerKey    = "analytics_schema"
	SensitiveKey = "sensitive"
	EventKey     = "event"
	severityKey  = "severity"
	messageKey   = "message"
)

var reservedKeys = []string{MarkerKey, SensitiveKey, EventKey, severityKey, messageKey}

// Logger writes events as JSON lines.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLogger writes to w: os.Stdout on Cloud Run, a Memory everywhere else.
func NewLogger(w io.Writer) *Logger { return &Logger{w: w} }

// Emit writes e as one line. Call it only after the work e reports has
// succeeded, so that a count never includes something that failed.
//
// A failure is logged and swallowed: the caller's work already succeeded, and a
// lost count is not worth a failed request.
func (l *Logger) Emit(e Event) {
	if l == nil {
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		log.Printf("analytics: marshal %s: %v", e.EventName(), err)
		return
	}
	// RawMessage keeps each value exactly as marshalled; decoding into any would
	// turn every integer into a float.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		log.Printf("analytics: reread %s: %v", e.EventName(), err)
		return
	}
	name, _ := json.Marshal(e.EventName())
	schema, _ := json.Marshal(Schema)
	sensitive, _ := json.Marshal(IsSensitive(e))
	fields[severityKey] = json.RawMessage(`"INFO"`)
	fields[messageKey] = name
	fields[EventKey] = name
	fields[MarkerKey] = schema
	fields[SensitiveKey] = sensitive

	line, err := json.Marshal(fields)
	if err != nil {
		log.Printf("analytics: write %s: %v", e.EventName(), err)
		return
	}
	// Requests are served concurrently, and two lines interleaved on stdout are
	// two entries Cloud Logging cannot parse.
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(append(line, '\n')); err != nil {
		log.Printf("analytics: write %s: %v", e.EventName(), err)
	}
}
