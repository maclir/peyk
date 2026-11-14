package laptop

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// tailBytes bounds how much of a transcript is read. The last reply is at the
// end, and transcripts of long sessions reach tens of megabytes.
const tailBytes = 4 << 20

var unsafePathChars = regexp.MustCompile(`[^A-Za-z0-9]`)

// TranscriptDir is where Claude Code keeps the transcripts for a working
// directory: every character outside [A-Za-z0-9] in the path becomes "-".
func TranscriptDir(claudeDir, workdir string) string {
	return filepath.Join(claudeDir, "projects", unsafePathChars.ReplaceAllString(workdir, "-"))
}

// recentTranscripts is how many of a directory's newest transcripts are read.
// After /clear Claude Code starts a new transcript that aoe may not know yet,
// and the transcript just started holds no reply yet, so the newest one alone
// is not enough.
const recentTranscripts = 3

// LastMessage returns the latest text reply among the session's own transcript
// and the newest transcripts of its working directory.
func LastMessage(claudeDir, workdir, agentSessionID string) *session.Message {
	dir := TranscriptDir(claudeDir, workdir)
	candidates := newestFirst(dir)
	if len(candidates) > recentTranscripts {
		candidates = candidates[:recentTranscripts]
	}
	if agentSessionID != "" {
		candidates = append(candidates, filepath.Join(dir, agentSessionID+".jsonl"))
	}
	var best *session.Message
	for _, path := range candidates {
		if m := lastInFile(path); m != nil && (best == nil || m.At.After(best.At)) {
			best = m
		}
	}
	return best
}

func newestFirst(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

type entry struct {
	Type        string    `json:"type"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	Message     struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func lastInFile(path string) *session.Message {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	start := info.Size() - tailBytes
	if start < 0 {
		start = 0
	}
	data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if err != nil {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line was cut by the offset
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], []byte(`"type":"assistant"`)) {
			continue
		}
		var e entry
		if json.Unmarshal(lines[i], &e) != nil || e.Type != "assistant" || e.IsSidechain {
			continue
		}
		var parts []string
		for _, c := range e.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		if len(parts) > 0 {
			return &session.Message{Text: strings.Join(parts, "\n\n"), At: e.Timestamp}
		}
	}
	return nil
}
