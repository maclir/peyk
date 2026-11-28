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

// FindTranscript locates the conversation id for workdir among claudeDirs:
// the directory holding that transcript, else the one with the directory's
// newest transcript, else the first.
func FindTranscript(claudeDirs []string, workdir, id string) session.Transcript {
	t := session.Transcript{ID: id, Workdir: workdir}
	var latest time.Time
	for _, c := range claudeDirs {
		dir := TranscriptDir(c, workdir)
		if t.Dir == "" {
			t.Dir = dir
		}
		if id != "" {
			if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err == nil {
				t.Dir = dir
				return t
			}
		}
		if mod := newestMod(dir); mod.After(latest) {
			latest, t.Dir = mod, dir
		}
	}
	return t
}

func newestMod(dir string) time.Time {
	paths := newestFirst(dir)
	if len(paths) == 0 {
		return time.Time{}
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// recentTranscripts is how many of a directory's newest transcripts are read
// for a session whose own transcript aoe has not recorded.
const recentTranscripts = 3

// LastExchange returns the agent's latest text reply, the last prompt a
// person sent it, and the model the conversation last used. A session's own
// transcript is authoritative: two sessions can share a directory, and a
// transcript just started by /clear rightly has none yet. Only without a
// recorded transcript does it fall back to the directory's newest ones, taking
// the one with the latest activity.
func LastExchange(t session.Transcript) (reply, prompt *session.Message, model string) {
	if t.Tool == session.Antigravity {
		return antigravityExchange(t)
	}
	if t.ID != "" {
		return lastInFile(filepath.Join(t.Dir, t.ID+".jsonl"))
	}
	candidates := newestFirst(t.Dir)
	if len(candidates) > recentTranscripts {
		candidates = candidates[:recentTranscripts]
	}
	var latest time.Time
	for _, path := range candidates {
		r, p, m := lastInFile(path)
		if at := newest(r, p); at.After(latest) {
			latest, reply, prompt, model = at, r, p, m
		} else if model == "" && m != "" && latest.IsZero() {
			model = m
		}
	}
	return reply, prompt, model
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
	UUID        string    `json:"uuid"`
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
		// Model is the model that wrote an assistant entry.
		Model string `json:"model"`
	} `json:"message"`
	// Attachment holds a prompt sent while the agent was working, which
	// Claude Code records as a queued command instead of a user entry.
	Attachment struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"`
		Origin struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	} `json:"attachment"`
}

// humanPrompt returns the text of a prompt a person sent, or "" when the
// entry is not one.
func (e entry) humanPrompt() string {
	if e.IsSidechain {
		return ""
	}
	switch {
	case e.Type == "user" && e.Origin.Kind == "human":
		return e.text()
	case e.Type == "user":
		return e.shellPrompt()
	case e.Type == "attachment" && e.Attachment.Type == "queued_command" && e.Attachment.Origin.Kind == "human":
		return contentText(e.Attachment.Prompt)
	}
	return ""
}

// bashInput is how Claude Code records a "!" shell command a person sent.
// The entry carries no human origin.
var bashInput = []byte("<bash-input>")

// shellPrompt returns a "!" command a person sent, written as they typed it,
// or "" when the entry is not one. A prompt peyk types after a "!" left in
// the terminal's input arrives this way too.
func (e entry) shellPrompt() string {
	var s string
	if json.Unmarshal(e.Message.Content, &s) != nil {
		return ""
	}
	cmd, ok := strings.CutPrefix(s, string(bashInput))
	if !ok {
		return ""
	}
	cmd, _ = strings.CutSuffix(cmd, "</bash-input>")
	return "! " + strings.TrimSpace(cmd)
}

type part struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// text joins the entry's non-empty text, or returns "" when it has none.
func (e entry) text() string { return contentText(e.Message.Content) }

// contentText joins a message content's non-empty text: a plain string or a
// list of parts.
func contentText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []part
	if json.Unmarshal(content, &parts) != nil {
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
		if json.Unmarshal(line, &e) != nil || e.IsSidechain {
			continue
		}
		if e.Type == "user" && bytes.Contains(line, clearMarker) {
			return e.Timestamp, true
		}
		if e.humanPrompt() != "" {
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}

// lastInFile reports the /clear that started the transcript as its prompt
// when a person has sent nothing since, and reads the model from the latest
// non-synthetic assistant entry even before a text reply is written.
func lastInFile(path string) (reply, prompt *session.Message, model string) {
	lines := tail(path)
	var cleared *session.Message
	for i := len(lines) - 1; i >= 0 && (reply == nil || prompt == nil || model == ""); i-- {
		if cleared == nil && bytes.Contains(lines[i], clearMarker) {
			var e entry
			if json.Unmarshal(lines[i], &e) == nil && e.Type == "user" && !e.IsSidechain {
				cleared = &session.Message{ID: e.UUID, Text: session.ClearCommand, At: e.Timestamp}
			}
			continue
		}
		isAssistant := (reply == nil || model == "") && bytes.Contains(lines[i], []byte(`"type":"assistant"`))
		isPrompt := prompt == nil && (bytes.Contains(lines[i], []byte(`"kind":"human"`)) || bytes.Contains(lines[i], bashInput))
		if !isAssistant && !isPrompt {
			continue
		}
		var e entry
		if json.Unmarshal(lines[i], &e) != nil || e.IsSidechain {
			continue
		}
		if isAssistant && e.Type == "assistant" {
			if model == "" {
				model = replyModel(e.Message.Model)
			}
			if reply == nil {
				if text := e.text(); text != "" {
					reply = &session.Message{ID: e.UUID, Text: text, At: e.Timestamp, Model: replyModel(e.Message.Model)}
				}
			}
		} else if isPrompt {
			if text := e.humanPrompt(); text != "" {
				prompt = &session.Message{ID: e.UUID, Text: text, At: e.Timestamp}
			}
		}
	}
	if prompt == nil {
		prompt = cleared
	}
	if reply != nil && reply.Model == "" {
		reply.Model = model
	}
	return reply, prompt, model
}

// replyModel leaves out the placeholder Claude Code records for a reply no
// model wrote, such as an error it reports itself.
func replyModel(m string) string {
	if m == "<synthetic>" {
		return ""
	}
	return m
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
	if t.ID == "" || t.Tool == session.Antigravity {
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

// launch is the part of a tool result that names a task the agent left
// running after its turn: a background shell, an async subagent, or a
// monitor. A persistent monitor watches for the rest of the session, so it
// never means the agent still has work in flight.
type launch struct {
	ToolUseResult struct {
		BackgroundTaskID string `json:"backgroundTaskId"`
		Status           string `json:"status"`
		AgentID          string `json:"agentId"`
		TaskID           string `json:"taskId"`
		Persistent       bool   `json:"persistent"`
	} `json:"toolUseResult"`
}

func (l launch) id() string {
	r := l.ToolUseResult
	switch {
	case r.BackgroundTaskID != "":
		return r.BackgroundTaskID
	case r.Status == "async_launched":
		return r.AgentID
	case r.TaskID != "" && !r.Persistent:
		return r.TaskID
	}
	return ""
}

var taskEnded = regexp.MustCompile(`<task-id>([A-Za-z0-9_-]+)</task-id>`)

// stopTool ends a task without Claude Code writing a notification for it.
const stopTool = "TaskStop"

type stopCall struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		TaskID string `json:"task_id"`
	} `json:"input"`
}

// stopped returns the tasks an assistant entry stops.
func stopped(line []byte) []string {
	var e entry
	var calls []stopCall
	if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.Type != "assistant" || json.Unmarshal(e.Message.Content, &calls) != nil {
		return nil
	}
	var ids []string
	for _, c := range calls {
		if c.Type == "tool_use" && c.Name == stopTool && c.Input.TaskID != "" {
			ids = append(ids, c.Input.TaskID)
		}
	}
	return ids
}

// Backgrounded says whether the agent launched a task that has not ended:
// Claude Code has not reported it and the agent has not stopped it. Claude Code wakes the agent when one ends, so its
// turn finishing does not mean its work has. Only the session's own
// transcript is read, because only its own process runs its tasks.
func Backgrounded(t session.Transcript) bool {
	if t.ID == "" {
		return false
	}
	if t.Tool == session.Antigravity {
		return antigravityBackgrounded(t)
	}
	running := map[string]bool{}
	for _, line := range tail(filepath.Join(t.Dir, t.ID+".jsonl")) {
		for _, m := range taskEnded.FindAllSubmatch(line, -1) {
			delete(running, string(m[1]))
		}
		if bytes.Contains(line, []byte(`"name":"`+stopTool+`"`)) {
			for _, id := range stopped(line) {
				delete(running, id)
			}
		}
		if !bytes.Contains(line, []byte(`"toolUseResult"`)) {
			continue
		}
		var e entry
		var l launch
		if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.Type != "user" || json.Unmarshal(line, &l) != nil {
			continue
		}
		if id := l.id(); id != "" {
			running[id] = true
		}
	}
	return len(running) > 0
}

// Progress returns what a working session is currently doing in its latest
// turn: its checklist, latest tool call, and (for Antigravity) thinking
// summary. Returns nil when no progress has been recorded after the latest
// prompt.
func Progress(t session.Transcript) *session.Progress {
	if t.Tool == session.Antigravity {
		return antigravityProgress(t)
	}
	return claudeProgress(t)
}

func claudeProgress(t session.Transcript) *session.Progress {
	path := filepath.Join(t.Dir, t.ID+".jsonl")
	if t.ID == "" {
		newest := newestFirst(t.Dir)
		if len(newest) == 0 {
			return nil
		}
		path = newest[0]
	}
	lines := tail(path)
	var prog session.Progress
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if bytes.Contains(line, clearMarker) {
			var e entry
			if json.Unmarshal(line, &e) == nil && e.Type == "user" && !e.IsSidechain {
				break
			}
		}
		isPrompt := bytes.Contains(line, []byte(`"kind":"human"`)) || bytes.Contains(line, bashInput)
		isAssistant := bytes.Contains(line, []byte(`"type":"assistant"`))
		if !isPrompt && !isAssistant {
			continue
		}
		var e entry
		if json.Unmarshal(line, &e) != nil || e.IsSidechain {
			continue
		}
		if isPrompt && e.humanPrompt() != "" {
			break
		}
		if !isAssistant || e.Type != "assistant" {
			continue
		}
		var parts []contentPart
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			continue
		}
		for j := len(parts) - 1; j >= 0; j-- {
			p := parts[j]
			if p.Type != "tool_use" || p.Name == "" {
				continue
			}
			if p.Name == "TodoWrite" {
				if len(prog.Steps) == 0 {
					if steps := parseClaudeTodos(p.Input["todos"]); len(steps) > 0 {
						prog.Steps = steps
						if prog.At.IsZero() {
							prog.At = e.Timestamp
						}
					}
				}
				continue
			}
			if prog.Tool == "" {
				prog.Tool = p.Name
				prog.Detail = shortToolDetail(toolLine(p.Input), t.Workdir)
				if prog.At.IsZero() {
					prog.At = e.Timestamp
				}
			}
		}
		if prog.Tool != "" && len(prog.Steps) > 0 {
			break
		}
	}
	if prog.Tool == "" && len(prog.Steps) == 0 {
		return nil
	}
	return &prog
}

func parseClaudeTodos(raw json.RawMessage) []session.Step {
	if len(raw) == 0 {
		return nil
	}
	var todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(raw, &todos) != nil {
		return nil
	}
	out := make([]session.Step, 0, len(todos))
	for _, td := range todos {
		title := strings.Join(strings.Fields(td.Content), " ")
		if title == "" {
			continue
		}
		if r := []rune(title); len(r) > maxToolLine {
			title = string(r[:maxToolLine-1]) + "…"
		}
		status := td.Status
		if status == "completed" {
			status = "done"
		}
		out = append(out, session.Step{Title: title, Status: status})
	}
	return out
}

// shortToolDetail trims a session's working directory prefix from file paths
// so narrow phone cards show project-relative paths.
func shortToolDetail(detail, workdir string) string {
	if detail == "" || workdir == "" {
		return detail
	}
	prefix := strings.TrimRight(workdir, string(filepath.Separator)) + string(filepath.Separator)
	if rest, ok := strings.CutPrefix(detail, prefix); ok && rest != "" {
		return rest
	}
	return detail
}
