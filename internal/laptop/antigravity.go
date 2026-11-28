package laptop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	return session.Transcript{Tool: session.Antigravity, AppDir: appDir, Workdir: workdir}.WithID(id)
}

// LatestAntigravityConversation is the conversation Antigravity CLI last
// started in workdir, or "" when it has started none.
func LatestAntigravityConversation(appDir, workdir string) string {
	if appDir == "" {
		return ""
	}
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

// antigravityConversations returns the conversation directories present under
// appDir/brain.
func antigravityConversations(appDir string) map[string]bool {
	if appDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(appDir, "brain"))
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			seen[e.Name()] = true
		}
	}
	return seen
}

// newAntigravityConversation returns the newest conversation directory under
// appDir/brain that was not in before, preferring one with no transcript yet.
// Antigravity creates brain/<id> immediately on /clear and updates
// cache/last_conversations.json only once the conversation gets a prompt.
func newAntigravityConversation(appDir string, before map[string]bool, oldID string) string {
	if appDir == "" {
		return ""
	}
	brain := filepath.Join(appDir, "brain")
	entries, err := os.ReadDir(brain)
	if err != nil {
		return ""
	}
	var best string
	var bestEmpty bool
	var bestMod time.Time
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || before[name] || name == oldID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		_, err = os.Stat(filepath.Join(brain, name, ".system_generated", "logs", "transcript.jsonl"))
		empty := os.IsNotExist(err)
		if best == "" || (empty && !bestEmpty) || (empty == bestEmpty && info.ModTime().After(bestMod)) {
			best, bestEmpty, bestMod = name, empty, info.ModTime()
		}
	}
	return best
}

// step is one line of an Antigravity transcript: a prompt, a reply, a tool
// call or its result. Each step's index is its id within the conversation.
type step struct {
	Index     json.Number `json:"step_index"`
	Source    string      `json:"source"`
	Type      string      `json:"type"`
	Model     string      `json:"model"`
	CreatedAt time.Time   `json:"created_at"`
	Content   string      `json:"content"`
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
// model comes from the newest planner response or prompt model-selection
// record, even before the model writes its first reply or after the opening
// prompt scrolls past the tail window.
func antigravityExchange(t session.Transcript) (reply, prompt *session.Message, model string) {
	if t.ID == "" {
		return nil, nil, ""
	}
	lines := tail(antigravityTranscript(t))
	for i := len(lines) - 1; i >= 0 && (reply == nil || prompt == nil || model == ""); i-- {
		var s step
		if json.Unmarshal(lines[i], &s) != nil {
			continue
		}
		switch {
		case s.Source == "MODEL" && s.Type == "PLANNER_RESPONSE":
			if model == "" && s.Model != "" {
				model = s.Model
			}
			if reply == nil && strings.TrimSpace(s.Content) != "" {
				text := s.Content
				if contains(s.Truncated, "content") {
					text = fullContent(t, s.Index.String(), text)
				}
				reply = &session.Message{ID: s.Index.String(), Text: strings.TrimSpace(text), At: s.CreatedAt}
			}
		case s.prompt():
			if prompt == nil {
				prompt = &session.Message{ID: s.Index.String(), Text: promptText(s.Content), At: s.CreatedAt}
			}
			if m := modelChange.FindStringSubmatch(s.Content); m != nil && model == "" {
				model = m[1]
			}
		}
	}
	if reply != nil {
		reply.Model = model
	}
	return reply, prompt, model
}

var (
	agTaskStarted  = regexp.MustCompile(`Tool is running as a background task with task id: (\S+)`)
	agTaskFinished = regexp.MustCompile(`Task id "([^"]+)" (?:finished|failed|cancelled|killed|exited)`)
)

// antigravityBackgrounded reports whether an Antigravity conversation has a
// non-daemon background task that has not finished or been killed.
func antigravityBackgrounded(t session.Transcript) bool {
	if t.ID == "" {
		return false
	}
	running := map[string]bool{}
	var allDaemons bool
	for _, line := range tail(antigravityTranscript(t)) {
		var s step
		if json.Unmarshal(line, &s) != nil {
			continue
		}
		switch {
		case s.Source == "MODEL" && s.Type == "PLANNER_RESPONSE":
			var bgCalls, daemonCalls int
			for _, c := range s.ToolCalls {
				if c.Name == "run_command" || c.Name == "schedule" {
					bgCalls++
					if argBool(c.Args["IsDaemon"]) {
						daemonCalls++
					}
				}
				if c.Name == "manage_task" && argString(c.Args["Action"]) == "kill" {
					if id := argString(c.Args["TaskId"]); id != "" {
						delete(running, id)
					}
				}
			}
			allDaemons = bgCalls > 0 && bgCalls == daemonCalls
		case s.Source == "MODEL" && s.Type == "GENERIC":
			if !allDaemons {
				if m := agTaskStarted.FindStringSubmatch(s.Content); m != nil {
					running[m[1]] = true
				}
			}
		case s.Source == "SYSTEM" && s.Type == "SYSTEM_MESSAGE":
			for _, m := range agTaskFinished.FindAllStringSubmatch(s.Content, -1) {
				delete(running, m[1])
			}
		}
	}
	return len(running) > 0
}

func argString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	var inner string
	if json.Unmarshal([]byte(s), &inner) == nil {
		return inner
	}
	return s
}

func argBool(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return argString(raw) == "true"
}

// antigravityStepStatus derives an Antigravity session's status from the last
// step of its transcript: "idle" once it has replied without calling a tool
// (or before any step is recorded), "waiting" while a tool call has no result
// yet, and "running" while the model is working after a prompt, tool result,
// or system message. toolCall is true when waiting on a non-question tool call
// that may still be running rather than awaiting approval.
func antigravityStepStatus(t session.Transcript) (status string, toolCall bool) {
	if t.ID == "" {
		return "idle", false
	}
	lines := tail(antigravityTranscript(t))
	for i := len(lines) - 1; i >= 0; i-- {
		var s step
		if json.Unmarshal(lines[i], &s) != nil {
			continue
		}
		if s.reply() && len(s.ToolCalls) == 0 {
			return "idle", false
		}
		if len(s.ToolCalls) > 0 {
			for _, c := range s.ToolCalls {
				if c.Name == "ask_question" {
					return "waiting", false
				}
			}
			return "waiting", true
		}
		return "running", false
	}
	return "idle", false
}

// AntigravityAsking reports whether the last step in t is an open
// ask_question tool call waiting for the user.
func AntigravityAsking(t session.Transcript) bool {
	status, toolCall := antigravityStepStatus(t)
	return status == "waiting" && !toolCall
}

// antigravityRunning reports whether an Antigravity session's tmux pane shows
// an active spinner and "esc to cancel", meaning a tool call is running
// rather than waiting on an approval prompt.
func (a Aoe) antigravityRunning(ctx context.Context, id string) bool {
	pane, err := a.pane(ctx, id)
	if err != nil {
		return false
	}
	screen, err := a.Run(ctx, "", "tmux", "capture-pane", "-p", "-t", pane)
	if err != nil {
		return false
	}
	return antigravityPaneRunning(screen)
}

func antigravityPaneRunning(screen []byte) bool {
	lines := strings.Split(string(screen), "\n")
	var bottom []string
	for i := len(lines) - 1; i >= 0 && len(bottom) < 8; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			bottom = append(bottom, line)
		}
	}
	var hasCancel, hasSpinner bool
	for _, line := range bottom {
		if strings.Contains(line, "esc to cancel") {
			hasCancel = true
		}
		for _, r := range line {
			if r >= 0x2801 && r <= 0x28ff {
				hasSpinner = true
			}
			break
		}
	}
	return hasCancel && hasSpinner
}

// fullContent finds a step's whole content in transcript_full.jsonl, or
// keeps the cut one when that file does not have it.
func fullContent(t session.Transcript, index, cut string) string {
	f, err := os.Open(filepath.Join(t.Dir, "transcript_full.jsonl"))
	if err != nil {
		return cut
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(index)) {
			var s step
			if json.Unmarshal(line, &s) == nil && s.Index.String() == index {
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
					text = fullContent(t, s.Index.String(), text)
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

// AntigravityModels lists the models `agy models` says the account can
// run, one "id<TAB>name" line each.
func AntigravityModels(ctx context.Context, run Runner, agy string) ([]session.Model, error) {
	out, err := run(ctx, "", agy, "models")
	if err != nil {
		return nil, err
	}
	var models []session.Model
	for _, line := range strings.Split(string(out), "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && id != "" && name != "" {
			models = append(models, session.Model{ID: id, Name: strings.TrimSpace(name)})
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("agy models listed none: %s", strings.TrimSpace(string(out)))
	}
	return models, nil
}
