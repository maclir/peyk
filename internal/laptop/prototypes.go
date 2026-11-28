package laptop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// Prototypes finds the pages a conversation published: Claude Code's Artifact
// tool results from its transcript, and self-hosted pages recorded by
// `peyk publish` in Path. A transcript can grow to tens of megabytes, so it
// remembers how far it has read each transcript and reads only what was
// appended since.
type Prototypes struct {
	mu   sync.Mutex
	seen map[string]*scanned
	// Path, when set, holds pages published with `peyk publish`, keyed by
	// conversation id.
	Path string
}

type scanned struct {
	offset int64
	found  map[string]session.Prototype
}

// Find returns the pages a session's own conversation published, newest first.
func (p *Prototypes) Find(t session.Transcript) []session.Prototype {
	if t.ID == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	found := map[string]session.Prototype{}
	if t.Tool == "" && t.Dir != "" {
		path := filepath.Join(t.Dir, t.ID+".jsonl")
		if p.seen == nil {
			p.seen = map[string]*scanned{}
		}
		s := p.seen[path]
		if s == nil {
			s = &scanned{found: map[string]session.Prototype{}}
			p.seen[path] = s
		}
		s.scan(path)
		for url, proto := range s.found {
			found[url] = proto
		}
	}
	if p.Path != "" {
		if saved, err := loadPublishedPrototypes(p.Path); err == nil {
			for _, proto := range saved[t.ID] {
				if prev, ok := found[proto.URL]; !ok || !proto.At.Before(prev.At) {
					found[proto.URL] = proto
				}
			}
		}
	}
	return newestPrototypes(found)
}

// Record persists a self-hosted prototype under conversationID so refreshes
// and restarts keep it on that conversation.
func (p *Prototypes) Record(conversationID string, proto session.Prototype) error {
	if p.Path == "" || conversationID == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	saved, err := loadPublishedPrototypes(p.Path)
	if err != nil {
		return err
	}
	byURL := map[string]session.Prototype{}
	for _, existing := range saved[conversationID] {
		byURL[existing.URL] = existing
	}
	if prev, ok := byURL[proto.URL]; !ok || !proto.At.Before(prev.At) {
		byURL[proto.URL] = proto
	}
	saved[conversationID] = newestPrototypes(byURL)
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return err
	}
	tmp := p.Path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.Path)
}

// LatestHostedID returns the 64-hex id of the newest self-hosted `/p/<id>`
// prototype recorded for conversationID, or "" when none exists.
func (p *Prototypes) LatestHostedID(conversationID string) string {
	if p.Path == "" || conversationID == "" {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	saved, err := loadPublishedPrototypes(p.Path)
	if err != nil {
		return ""
	}
	for _, proto := range saved[conversationID] {
		if id, ok := strings.CutPrefix(proto.URL, "/p/"); ok && isHexID(id) {
			return id
		}
	}
	return ""
}

func isHexID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func loadPublishedPrototypes(path string) (map[string][]session.Prototype, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]session.Prototype{}, nil
	}
	if err != nil {
		return nil, err
	}
	saved := map[string][]session.Prototype{}
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	return saved, nil
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

// designType is the Design Artifact type. A publish result names the type
// its artifact was made from; a plain HTML page names none.
const designType = "https://claude.ai/artifact/QKN21svewxgyPb6SYRqWnd"

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
		Type       *struct {
			URL string `json:"url"`
		} `json:"type"`
	}
	if json.Unmarshal(e.Result, &r) != nil || r.ArtifactID == "" || r.Version == "" || !strings.HasPrefix(r.URL, "https://claude.ai/") {
		return session.Prototype{}, false
	}
	kind := session.PrototypeHTML
	if r.Type != nil {
		kind = session.PrototypeOther
		if r.Type.URL == designType {
			kind = session.PrototypeDesign
		}
	}
	return session.Prototype{URL: r.URL, Title: r.Title, Kind: kind, At: e.Timestamp}, true
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
