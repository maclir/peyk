package laptop

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Prototypes finds the pages a transcript published with Claude Code's
// Artifact tool. A page can be published early in a conversation that later
// grows to tens of megabytes, so it remembers how far it has read each
// transcript and reads only what was appended since.
type Prototypes struct {
	mu   sync.Mutex
	seen map[string]*scanned
}

type scanned struct {
	offset int64
	found  map[string]session.Prototype
}

// Find returns the pages a session's own transcript published, newest first.
func (p *Prototypes) Find(t session.Transcript) []session.Prototype {
	if t.ID == "" {
		return nil
	}
	path := filepath.Join(t.Dir, t.ID+".jsonl")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]*scanned{}
	}
	s := p.seen[path]
	if s == nil {
		s = &scanned{found: map[string]session.Prototype{}}
		p.seen[path] = s
	}
	s.scan(path)
	return newestPrototypes(s.found)
}

// scan reads complete lines appended since the last scan. A transcript that
// shrank was replaced, so it is read again from the start.
func (s *scanned) scan(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return
	}
	if info.Size() < s.offset {
		s.offset, s.found = 0, map[string]session.Prototype{}
	}
	data, err := io.ReadAll(io.NewSectionReader(f, s.offset, info.Size()-s.offset))
	if err != nil {
		return
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return
	}
	for line := range bytes.SplitSeq(data[:end], []byte("\n")) {
		if p, ok := publishedPrototype(line); ok {
			if prev, seen := s.found[p.URL]; !seen || !p.At.Before(prev.At) {
				s.found[p.URL] = p
			}
		}
	}
	s.offset += int64(end + 1)
}

// publishedPrototype reads an Artifact publish result. Reads and listings of
// artifacts carry no version, so only a publish yields a prototype.
func publishedPrototype(line []byte) (session.Prototype, bool) {
	if !bytes.Contains(line, []byte(`"toolUseResult"`)) || !bytes.Contains(line, []byte(`"artifact_id"`)) {
		return session.Prototype{}, false
	}
	var e struct {
		Timestamp time.Time       `json:"timestamp"`
		Result    json.RawMessage `json:"toolUseResult"`
	}
	if json.Unmarshal(line, &e) != nil {
		return session.Prototype{}, false
	}
	var r struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		ArtifactID string `json:"artifact_id"`
		Version    string `json:"version"`
	}
	if json.Unmarshal(e.Result, &r) != nil || r.ArtifactID == "" || r.Version == "" || !strings.HasPrefix(r.URL, "https://claude.ai/") {
		return session.Prototype{}, false
	}
	return session.Prototype{URL: r.URL, Title: r.Title, At: e.Timestamp}, true
}

func newestPrototypes(found map[string]session.Prototype) []session.Prototype {
	if len(found) == 0 {
		return nil
	}
	out := make([]session.Prototype, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].URL < out[j].URL
	})
	if len(out) > session.MaxPrototypes {
		out = out[:session.MaxPrototypes]
	}
	return out
}
