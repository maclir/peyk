// Package idea turns a task into an ideation run and reads the ideas the run
// reports back.
//
// The run reports its ideas in a fenced block at the end of its final reply,
// so they reach the laptop through the transcript it already reads: no
// command, socket or file that a sandboxed session might not be allowed to use.
package idea

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Lens is one kind of improvement a run looks for. The instruction is fixed on
// the laptop, so the signed lens ids alone decide what is asked.
type Lens struct {
	ID   string
	Name string
	Ask  string
}

var Lenses = []Lens{
	{"reuse", "Reuse", "the same logic, component or style written more than once that could live in one place"},
	{"experience", "One experience", "the same concept that looks, reads or behaves differently in different places, such as wording, layout, confirmations, empty states or gestures"},
	{"architecture", "Architecture", "boundaries, dependencies or data flow that make changes harder than they need to be"},
	{"security", "Security", "ways an attacker, a leaked credential or untrusted input could do harm"},
	{"bugs", "Bugs", "behaviour that is wrong for a concrete input or state"},
	{"features", "Features", "small features the existing code almost supports and a user would notice"},
	{"simplify", "Simplify", "dead code, needless indirection, or abstractions with one caller"},
	{"tests", "Tests", "behaviour no test covers, or tests that are slow or flaky"},
	{"performance", "Performance & cost", "work, reads, writes or startup time that could be avoided"},
	{"accessibility", "Accessibility", "controls, contrast, labels or focus that fail a screen reader, keyboard or small screen"},
	{"invariants", "Invariant drift", "code that breaks a rule in the repository's agent instructions, or a rule the code no longer follows"},
}

func lens(id string) (Lens, bool) {
	for _, l := range Lenses {
		if l.ID == id {
			return l, true
		}
	}
	return Lens{}, false
}

// Limits the phone offers for how many ideas one run reports.
var Limits = []int{3, 5, 10}

// Ask is what a task asks a run to look for.
type Ask struct {
	Lenses []string `firestore:"lenses" json:"lenses"`
	Max    int      `firestore:"max" json:"max"`
}

func (a Ask) check() error {
	if len(a.Lenses) == 0 {
		return errors.New("ideation needs at least one lens")
	}
	for _, id := range a.Lenses {
		if _, ok := lens(id); !ok {
			return fmt.Errorf("no lens %q", id)
		}
	}
	if slices.Contains(Limits, a.Max) {
		return nil
	}
	return fmt.Errorf("ideation limit %d is not one of %v", a.Max, Limits)
}

func (a Ask) has(id string) bool { return slices.Contains(a.Lenses, id) }

// Idea statuses. An open idea waits on the phone; the phone dismisses it or
// adds it to the inbox. Neither is deleted, so a later run is told about them.
const (
	Open      = "open"
	Dismissed = "dismissed"
	Added     = "added"
)

// Idea is the Firestore document.
type Idea struct {
	Project string `firestore:"project"`
	// Task is the ideation task that found it.
	Task  string   `firestore:"task"`
	Lens  string   `firestore:"lens"`
	Title string   `firestore:"title"`
	Why   string   `firestore:"why"`
	Refs  []string `firestore:"refs"`
	// Prompt is the task text the idea becomes in the inbox.
	Prompt    string    `firestore:"prompt"`
	Status    string    `firestore:"status"`
	CreatedAt time.Time `firestore:"created_at"`
	// Note is why the phone dismissed it, for later runs to respect.
	Note string `firestore:"note,omitempty"`
	// AddedTask is the inbox task the idea became.
	AddedTask string `firestore:"added_task,omitempty"`
}

// Draft is one idea as a run reports it.
type Draft struct {
	Lens  string   `json:"lens"`
	Title string   `json:"title"`
	Why   string   `json:"why"`
	Refs  []string `json:"refs"`
	Task  string   `json:"task"`
}

// Fence tags the block a run ends its reply with.
const Fence = "peyk-ideas"

// maxKnown bounds the earlier ideas a prompt lists, newest kept.
const maxKnown = 60

// Prompt turns a task's text into the instruction for an ideation run. known
// are the project's earlier ideas, which the run must not propose again.
func Prompt(text string, ask Ask, known []Idea) (string, error) {
	if err := ask.check(); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nThis is an ideation task, not a code change. Read the repository and find at most %d improvements through these lenses:\n", text, ask.Max)
	for _, id := range ask.Lenses {
		l, _ := lens(id)
		fmt.Fprintf(&b, "- %s (%s): %s\n", l.Name, l.ID, l.Ask)
	}
	b.WriteString("\nGround every idea in code you read and cite its files and lines. Fewer strong ideas beat many weak ones; reporting fewer than the limit, or none, is fine. Do not edit, test or commit anything.\n")
	if len(known) > maxKnown {
		known = known[len(known)-maxKnown:]
	}
	if len(known) > 0 {
		b.WriteString("\nThese ideas were already reported. Do not report them again, and respect why any were dismissed:\n")
		for _, k := range known {
			switch {
			case k.Status == Dismissed && k.Note != "":
				fmt.Fprintf(&b, "- %s (dismissed: %s)\n", k.Title, k.Note)
			case k.Status == Dismissed:
				fmt.Fprintf(&b, "- %s (dismissed)\n", k.Title)
			default:
				fmt.Fprintf(&b, "- %s\n", k.Title)
			}
		}
	}
	fmt.Fprintf(&b, "\nEnd your final reply with one fenced code block tagged %s that holds a JSON array with one object per idea, and nothing after it:\n\n```%s\n", Fence, Fence)
	b.WriteString(`[{"lens": "` + ask.Lenses[0] + `", "title": "One line naming the problem", "why": "What is wrong, where, and why it matters", "refs": ["path/to/file.go:42"], "task": "The instruction a worker gets to make the change"}]`)
	b.WriteString("\n```\n\nlens is one of: " + strings.Join(ask.Lenses, ", ") + ".")
	return b.String(), nil
}

// Split separates the ideas block from the rest of a reply. A reply without
// one, or with one that does not parse, is returned whole with no drafts, so
// the phone still shows what the run said.
func Split(reply string) (string, []Draft) {
	open := "```" + Fence
	i := strings.LastIndex(reply, open)
	if i < 0 {
		return reply, nil
	}
	block, after, ok := strings.Cut(reply[i+len(open):], "\n```")
	if !ok {
		return reply, nil
	}
	var drafts []Draft
	if err := json.Unmarshal([]byte(block), &drafts); err != nil {
		return reply, nil
	}
	parts := []string{}
	for _, p := range []string{reply[:i], after} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	rest := strings.Join(parts, "\n\n")
	if drafts == nil {
		drafts = []Draft{}
	}
	return rest, drafts
}

// Field limits keep a document well inside Firestore's and the phone's means.
const (
	maxTitle = 200
	maxWhy   = 4000
	maxTask  = 8000
	maxRefs  = 10
	maxRef   = 300
)

// Accept keeps the drafts a run may report for ask and turns them into
// ideas: a lens it was asked for, a title and a task, a title not already
// known, and no more than its limit.
func Accept(drafts []Draft, ask Ask, known []Idea) []Idea {
	seen := map[string]bool{}
	for _, k := range known {
		seen[key(k.Title)] = true
	}
	var out []Idea
	for _, d := range drafts {
		if len(out) == ask.Max {
			break
		}
		title, task := clip(oneLine(d.Title), maxTitle), clip(strings.TrimSpace(d.Task), maxTask)
		if !ask.has(d.Lens) || title == "" || task == "" || seen[key(title)] {
			continue
		}
		seen[key(title)] = true
		var refs []string
		for _, r := range d.Refs {
			if r = clip(oneLine(r), maxRef); r != "" && len(refs) < maxRefs {
				refs = append(refs, r)
			}
		}
		out = append(out, Idea{Lens: d.Lens, Title: title, Why: clip(strings.TrimSpace(d.Why), maxWhy), Refs: refs, Prompt: task, Status: Open})
	}
	return out
}

func key(title string) string { return strings.ToLower(oneLine(title)) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
