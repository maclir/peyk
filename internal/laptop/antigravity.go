package laptop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// AntigravityDir is where Antigravity CLI keeps its conversations: each in
// brain/<conversation id>, and the latest per working directory in
// cache/last_conversations.json.
func AntigravityDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

// AntigravityTranscript locates a working directory's Antigravity
// conversation: id when aoe recorded one, else the latest the CLI started
// there.
func AntigravityTranscript(appDir, workdir, id string) session.Transcript {
	if id == "" {
		id = LatestAntigravityConversation(appDir, workdir)
	}
	t := session.Transcript{Tool: session.Antigravity, ID: id, AppDir: appDir, Workdir: workdir}
	if id != "" {
		t.Dir = filepath.Join(appDir, "brain", id, ".system_generated", "logs")
	}
	return t
}

// LatestAntigravityConversation is the conversation Antigravity CLI last
// started in workdir, or "" when it has started none.
func LatestAntigravityConversation(appDir, workdir string) string {
	b, err := os.ReadFile(filepath.Join(appDir, "cache", "last_conversations.json"))
	if err != nil {
		return ""
	}
	var latest map[string]string
	if json.Unmarshal(b, &latest) != nil {
		return ""
	}
	return latest[workdir]
}

// step is one line of an Antigravity transcript: a prompt, a reply, a tool
// call or its result. Each step's index is its id within the conversation.
type step struct {
	Index     string    `json:"step_index"`
	Source    string    `json:"source"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Content   string    `json:"content"`
	ToolCalls []struct {
		Name string                     `json:"name"`
		Args map[string]json.RawMessage `json:"args"`
	} `json:"tool_calls"`
	// Truncated names fields cut short in transcript.jsonl; the whole value
	// is in transcript_full.jsonl under the same step index.
	Truncated []string `json:"truncated_fields"`
}

func (s step) prompt() bool { return s.Source == "USER_EXPLICIT" && s.Type == "USER_INPUT" }
func (s step) reply() bool {
	return s.Source == "MODEL" && s.Type == "PLANNER_RESPONSE" && strings.TrimSpace(s.Content) != ""
}

// userRequest is what a person typed: Antigravity wraps a prompt in
// <USER_REQUEST> beside metadata it adds, such as the local time.
var userRequest = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)

func promptText(content string) string {
	if m := userRequest.FindStringSubmatch(content); m != nil {
		return m[1]
	}
	return strings.TrimSpace(content)
}

// modelChange is how a prompt records the model a person picked, in the
// name the CLI shows, such as "Gemini 3.8 Flash (High)".
var modelChange = regexp.MustCompile("changed setting `Model Selection` from .*? to (.+?)\\. ")

func antigravityTranscript(t session.Transcript) string {
	return filepath.Join(t.Dir, "transcript.jsonl")
}

// antigravityExchange is LastExchange for an Antigravity conversation. The
// reply names the model last picked in the conversation, if it says.
func antigravityExchange(t session.Transcript) (reply, prompt *session.Message) {
	if t.ID == "" {
		return nil, nil
	}
	lines := tail(antigravityTranscript(t))
	model := ""
	for i := len(lines) - 1; i >= 0 && (reply == nil || prompt == nil || model == ""); i-- {
		var s step
		if json.Unmarshal(lines[i], &s) != nil {
			continue
		}
		switch {
		case s.reply() && reply == nil:
			text := s.Content
			if contains(s.Truncated, "content") {
				text = fullContent(t, s.Index, text)
			}
			reply = &session.Message{ID: s.Index, Text: strings.TrimSpace(text), At: s.CreatedAt}
		case s.prompt():
			if prompt == nil {
				prompt = &session.Message{ID: s.Index, Text: promptText(s.Content), At: s.CreatedAt}
			}
			if m := modelChange.FindStringSubmatch(s.Content); m != nil && model == "" {
				model = m[1]
			}
		}
	}
	if reply != nil {
		reply.Model = model
	}
	return reply, prompt
}

// fullContent finds a step's whole content in transcript_full.jsonl, or
// keeps the cut one when that file does not have it.
func fullContent(t session.Transcript, index, cut string) string {
	f, err := os.Open(filepath.Join(t.Dir, "transcript_full.jsonl"))
	if err != nil {
		return cut
	}
	defer f.Close()
	want := []byte(`"step_index": "` + index + `"`)
	want2 := []byte(`"step_index":"` + index + `"`)
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, want) || bytes.Contains(line, want2) {
			var s step
			if json.Unmarshal(line, &s) == nil && s.Index == index {
				return s.Content
			}
		}
		if err != nil {
			return cut
		}
	}
}

// antigravityHistory is History for an Antigravity conversation. A tool call
// is named by the summary the CLI writes for it.
func antigravityHistory(t session.Transcript) (entries []session.Entry, truncated bool) {
	if t.ID == "" {
		return nil, false
	}
	f, err := os.Open(antigravityTranscript(t))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		var s step
		if json.Unmarshal(line, &s) == nil {
			switch {
			case s.prompt():
				entries = append(entries, session.Entry{Kind: session.EntryPrompt, Text: promptText(s.Content), At: s.CreatedAt})
			case s.reply():
				text := s.Content
				if contains(s.Truncated, "content") {
					text = fullContent(t, s.Index, text)
				}
				entries = append(entries, session.Entry{Kind: session.EntryReply, Text: strings.TrimSpace(text), At: s.CreatedAt})
			}
			for _, c := range s.ToolCalls {
				entries = append(entries, session.Entry{Kind: session.EntryTool, Tool: c.Name, Text: antigravityToolLine(c.Args), At: s.CreatedAt})
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
	}
	return bound(entries, MaxHistoryBytes)
}

// antigravityToolLine says what a tool call did. Antigravity encodes each
// argument as a JSON string inside the JSON object, so a value is decoded
// twice.
func antigravityToolLine(args map[string]json.RawMessage) string {
	decoded := map[string]json.RawMessage{}
	for k, v := range args {
		var inner string
		if json.Unmarshal(v, &inner) == nil && json.Valid([]byte(inner)) {
			decoded[k] = json.RawMessage(inner)
		} else {
			decoded[k] = v
		}
	}
	for _, k := range []string{"toolSummary", "CommandLine", "AbsolutePath", "TargetFile", "Query", "query", "Url"} {
		if line := toolLine(map[string]json.RawMessage{"description": decoded[k]}); line != "" {
			return line
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
