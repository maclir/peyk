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

// recentTranscripts is how many of a directory's newest transcripts are read
// for a session whose own transcript aoe has not recorded.
const recentTranscripts = 3

// LastExchange returns the agent's latest text reply and the last prompt a
// person sent it. A session's own transcript is authoritative: two sessions
// can share a directory, and a transcript just started by /clear rightly has
// neither yet. Only without a recorded transcript does it fall back to the
// directory's newest ones, taking the one with the latest activity.
func LastExchange(t session.Transcript) (reply, prompt *session.Message) {
	if t.ID != "" {
		return lastInFile(filepath.Join(t.Dir, t.ID+".jsonl"))
	}
	candidates := newestFirst(t.Dir)
	if len(candidates) > recentTranscripts {
		candidates = candidates[:recentTranscripts]
	}
	var latest time.Time
	for _, path := range candidates {
		r, p := lastInFile(path)
		if at := newest(r, p); at.After(latest) {
			latest, reply, prompt = at, r, p
		}
	}
	return reply, prompt
}

func newest(ms ...*session.Message) time.Time {
	var t time.Time
	for _, m := range ms {
		if m != nil && m.At.After(t) {
			t = m.At
		}
	}
	return t
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
	// Origin marks a prompt a person sent, as opposed to one Claude Code
	// added, such as a slash command's output or a tool result.
	Origin struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		// A string for a typed prompt, a list of parts otherwise.
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type part struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// text joins the entry's non-empty text, or returns "" when it has none.
func (e entry) text() string {
	var s string
	if json.Unmarshal(e.Message.Content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []part
	if json.Unmarshal(e.Message.Content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}

// tail returns the transcript's last tailBytes as whole lines.
func tail(path string) [][]byte {
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
	return lines
}

// clearMarker is how Claude Code records /clear at the start of the
// conversation it opens. The entry carries no human origin.
var clearMarker = []byte(`<command-name>/clear</command-name>`)

// headBytes bounds how much of a transcript's start is read for the /clear
// that opened it, which follows a few small bookkeeping entries.
const headBytes = 64 << 10

// ClearedSince returns the id of a transcript in dir that was opened by a
// /clear recorded at or after since, or "" when there is none.
func ClearedSince(dir string, since time.Time) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err != nil || info.ModTime().Before(since) {
			continue
		}
		if at, ok := openingClear(filepath.Join(dir, e.Name())); ok && !at.Before(since) {
			return strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	return ""
}

// openingClear returns when the /clear that opened a transcript was recorded.
func openingClear(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e entry
		if json.Unmarshal(line, &e) != nil || e.Type != "user" || e.IsSidechain {
			continue
		}
		if bytes.Contains(line, clearMarker) {
			return e.Timestamp, true
		}
		if e.Origin.Kind == "human" {
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}

// lastInFile reports the /clear that started the transcript as its prompt
// when a person has sent nothing since.
func lastInFile(path string) (reply, prompt *session.Message) {
	lines := tail(path)
	var cleared *session.Message
	for i := len(lines) - 1; i >= 0 && (reply == nil || prompt == nil); i-- {
		if cleared == nil && bytes.Contains(lines[i], clearMarker) {
			var e entry
			if json.Unmarshal(lines[i], &e) == nil && e.Type == "user" && !e.IsSidechain {
				cleared = &session.Message{Text: session.ClearCommand, At: e.Timestamp}
			}
			continue
		}
		isReply := reply == nil && bytes.Contains(lines[i], []byte(`"type":"assistant"`))
		isPrompt := prompt == nil && bytes.Contains(lines[i], []byte(`"kind":"human"`))
		if !isReply && !isPrompt {
			continue
		}
		var e entry
		if json.Unmarshal(lines[i], &e) != nil || e.IsSidechain {
			continue
		}
		text := e.text()
		if text == "" {
			continue
		}
		switch {
		case isReply && e.Type == "assistant":
			reply = &session.Message{Text: text, At: e.Timestamp}
		case isPrompt && e.Type == "user" && e.Origin.Kind == "human":
			prompt = &session.Message{Text: text, At: e.Timestamp}
		}
	}
	if prompt == nil {
		prompt = cleared
	}
	return reply, prompt
}

// askTool is the tool behind Claude Code's multiple-choice dialog.
const askTool = "AskUserQuestion"

type toolUse struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input struct {
		Questions []session.QuestionItem `json:"questions"`
	} `json:"input"`
}

// PendingQuestion returns the dialog the session's agent has open: its latest
// AskUserQuestion call that has no result yet. Only the session's own
// transcript is read, because a question is answered in that session alone.
func PendingQuestion(t session.Transcript) *session.Question {
	if t.ID == "" {
		return nil
	}
	lines := tail(filepath.Join(t.Dir, t.ID+".jsonl"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], []byte(`"name":"`+askTool+`"`)) || !bytes.Contains(lines[i], []byte(`"type":"tool_use"`)) {
			continue
		}
		var e entry
		if json.Unmarshal(lines[i], &e) != nil || e.IsSidechain || e.Type != "assistant" {
			continue
		}
		var parts []toolUse
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			continue
		}
		for _, p := range parts {
			if p.Type != "tool_use" || p.Name != askTool || len(p.Input.Questions) == 0 {
				continue
			}
			if hasResult(lines[i+1:], p.ID) {
				return nil
			}
			return &session.Question{ID: p.ID, At: e.Timestamp, Items: p.Input.Questions}
		}
		return nil
	}
	return nil
}

// Outcome says what became of a tool call: whether the transcript holds its
// result yet, and whether that result is a refusal, as when a dialog is
// dismissed with Esc.
func Outcome(t session.Transcript, id string) (done, refused bool) {
	if t.ID == "" {
		return false, false
	}
	line := resultLine(tail(filepath.Join(t.Dir, t.ID+".jsonl")), id)
	return line != nil, bytes.Contains(line, []byte(`"is_error":true`))
}

func hasResult(lines [][]byte, id string) bool { return resultLine(lines, id) != nil }

func resultLine(lines [][]byte, id string) []byte {
	needle := []byte(`"tool_use_id":"` + id + `"`)
	for _, l := range lines {
		if bytes.Contains(l, needle) {
			return l
		}
	}
	return nil
}
