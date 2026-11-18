package laptop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maclir/peyk/internal/session"
)

// MaxHistoryBytes bounds the text of a published history, oldest dropped
// first, so it stays inside one Firestore document (1 MiB).
const MaxHistoryBytes = 800 << 10

// maxToolLine bounds the line that names a tool call.
const maxToolLine = 160

// toolDetail lists the input fields that best say what a tool call did, in
// the order they are tried: a Bash call's command over its description, an
// Agent call's description over its prompt.
var toolDetail = []string{"command", "file_path", "notebook_path", "pattern", "url", "query", "description", "skill", "prompt"}

type contentPart struct {
	Type  string                     `json:"type"`
	Text  string                     `json:"text"`
	Name  string                     `json:"name"`
	Input map[string]json.RawMessage `json:"input"`
}

// History reads the session's whole conversation: the prompts a person sent,
// the agent's replies, and a line for each tool call. It reads the session's
// own transcript, or without one the directory's newest. Truncated says the
// oldest entries were dropped to fit MaxHistoryBytes.
func History(t session.Transcript) (entries []session.Entry, truncated bool) {
	path := filepath.Join(t.Dir, t.ID+".jsonl")
	if t.ID == "" {
		newest := newestFirst(t.Dir)
		if len(newest) == 0 {
			return nil, false
		}
		path = newest[0]
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		entries = append(entries, historyEntries(line)...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
	}
	return bound(entries, MaxHistoryBytes)
}

// historyEntries returns what one transcript line adds to the history. Tool
// results, which make up most of a transcript, are skipped before parsing.
func historyEntries(line []byte) []session.Entry {
	prompt := bytes.Contains(line, []byte(`"kind":"human"`))
	if !prompt && !bytes.Contains(line, []byte(`"type":"assistant"`)) {
		return nil
	}
	var e entry
	if json.Unmarshal(line, &e) != nil || e.IsSidechain {
		return nil
	}
	switch {
	case e.humanPrompt() != "":
		return []session.Entry{{Kind: session.EntryPrompt, Text: e.humanPrompt(), At: e.Timestamp}}
	case e.Type == "assistant":
		var parts []contentPart
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			return nil
		}
		var out []session.Entry
		for _, p := range parts {
			switch {
			case p.Type == "text" && strings.TrimSpace(p.Text) != "":
				out = append(out, session.Entry{Kind: session.EntryReply, Text: strings.TrimSpace(p.Text), At: e.Timestamp})
			case p.Type == "tool_use" && p.Name != "":
				out = append(out, session.Entry{Kind: session.EntryTool, Tool: p.Name, Text: toolLine(p.Input), At: e.Timestamp})
			}
		}
		return out
	}
	return nil
}

func toolLine(input map[string]json.RawMessage) string {
	for _, k := range toolDetail {
		var s string
		if json.Unmarshal(input[k], &s) != nil {
			continue
		}
		if s = strings.Join(strings.Fields(s), " "); s == "" {
			continue
		}
		if r := []rune(s); len(r) > maxToolLine {
			s = string(r[:maxToolLine-1]) + "…"
		}
		return s
	}
	return ""
}

func bound(entries []session.Entry, max int) ([]session.Entry, bool) {
	size := 0
	for i := len(entries) - 1; i >= 0; i-- {
		size += len(entries[i].Text) + len(entries[i].Tool)
		if size > max {
			return entries[i+1:], true
		}
	}
	return entries, false
}
