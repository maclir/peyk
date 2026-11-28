// Package session holds what peyk knows about one aoe coding session and how
// its state is derived. It does no I/O.
package session

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/maclir/peyk/internal/idea"
)

// State is what a card on the board shows, ordered by how urgently it needs you.
type State string

const (
	NeedsInput State = "needs_input"
	Error      State = "error"
	Dirty      State = "dirty"
	Unpushed   State = "unpushed"
	Working    State = "working"
	Idle       State = "idle"
	Stopped    State = "stopped"
)

// Rank orders states for the board: lower comes first.
func (s State) Rank() int {
	for i, v := range []State{NeedsInput, Error, Dirty, Unpushed, Idle, Working, Stopped} {
		if v == s {
			return i
		}
	}
	return 99
}

// Activity is when the card last changed in the way its group cares about:
// the newest commit for work waiting to be pushed, otherwise the newer of the
// latest reply and prompt.
func (s Snapshot) Activity() time.Time {
	if s.State == Unpushed && s.Git != nil && s.Git.Last != nil {
		return s.Git.Last.At
	}
	var t time.Time
	for _, m := range []*Message{s.Message, s.Prompt} {
		if m != nil && m.At.After(t) {
			t = m.At
		}
	}
	return t
}

// Compare orders the board: by state, then most recent activity, then title.
func Compare(a, b Snapshot) int {
	if d := a.State.Rank() - b.State.Rank(); d != 0 {
		return d
	}
	if c := b.Activity().Compare(a.Activity()); c != 0 {
		return c
	}
	return strings.Compare(a.Title, b.Title)
}

// Label is the state in the words the phone shows.
func (s State) Label() string {
	if s == NeedsInput {
		return "needs input"
	}
	return string(s)
}

// Commit is the most recent commit on the session's checked-out branch.
type Commit struct {
	Hash    string    `firestore:"hash" json:"hash"`
	Subject string    `firestore:"subject" json:"subject"`
	At      time.Time `firestore:"at" json:"at"`
}

// Git is the repository state of a session's working directory.
type Git struct {
	Branch string `firestore:"branch" json:"branch"`
	// Upstream is empty when the branch tracks no remote branch.
	Upstream string `firestore:"upstream" json:"upstream"`
	Unpushed int    `firestore:"unpushed" json:"unpushed"`
	// Pending lists the newest unpushed commits, at most MaxPending of them.
	Pending []Commit `firestore:"pending" json:"pending,omitempty"`
	Dirty   int      `firestore:"dirty" json:"dirty"`
	Last    *Commit  `firestore:"last" json:"last,omitempty"`
	// CI is the GitHub Actions result for Pushed.
	CI *CI `firestore:"ci" json:"ci,omitempty"`
	// Pushed is the full hash the upstream branch points at, and Repo the
	// owner/name of its GitHub repository when that repository has
	// workflows. They are the laptop's business and are not published.
	Pushed string `firestore:"-" json:"-"`
	Repo   string `firestore:"-" json:"-"`
}

// CIState is how a pushed commit's workflow runs went.
type CIState string

const (
	CIQueued    CIState = "queued"
	CIRunning   CIState = "running"
	CISuccess   CIState = "success"
	CIFailure   CIState = "failure"
	CICancelled CIState = "cancelled"
)

// Done is true once no run for the commit can change.
func (s CIState) Done() bool { return s == CISuccess || s == CIFailure || s == CICancelled }

// CI is the workflow run that best says how a pushed commit is doing: a
// failed one if any failed, else one still going, else a successful one.
type CI struct {
	SHA      string  `firestore:"sha" json:"sha"`
	Workflow string  `firestore:"workflow" json:"workflow"`
	State    CIState `firestore:"state" json:"state"`
	// URL is the run's page on GitHub.
	URL string `firestore:"url" json:"url"`
}

// MaxPending bounds the unpushed commits a snapshot carries, so a branch far
// ahead of its remote cannot grow the session document without limit.
const MaxPending = 20

// Message is the agent's latest reply, kept as the Markdown it wrote.
type Message struct {
	// ID is the transcript entry's uuid.
	ID   string    `firestore:"id" json:"id,omitempty"`
	Text string    `firestore:"text" json:"text"`
	At   time.Time `firestore:"at" json:"at"`
	// Model is the model that wrote a reply, such as "claude-opus-5-5".
	Model string `firestore:"model,omitempty" json:"model,omitempty"`
}

// Progress is what a working agent is currently doing, read from its
// transcript mid-turn. It is published only while State is Working and stays
// separate from Message so notifications, reply binding, and task completion
// still follow visible replies alone.
type Progress struct {
	// Tool and Detail name the latest tool call in the current turn.
	Tool   string `firestore:"tool,omitempty" json:"tool,omitempty"`
	Detail string `firestore:"detail,omitempty" json:"detail,omitempty"`
	// Thinking is the latest summarized thought in the current turn, before
	// any newer visible reply has landed (Antigravity).
	Thinking string `firestore:"thinking,omitempty" json:"thinking,omitempty"`
	// Steps is the current turn's checklist, in order.
	Steps []Step    `firestore:"steps,omitempty" json:"steps,omitempty"`
	At    time.Time `firestore:"at,omitempty" json:"at,omitempty"`
}

// Step is one item of a working agent's live checklist.
type Step struct {
	Title  string `firestore:"title" json:"title"`
	Status string `firestore:"status" json:"status"`
}

// Entry kinds in a conversation's history.
const (
	EntryPrompt = "prompt"
	EntryReply  = "reply"
	EntryTool   = "tool"
)

// Entry is one step of a conversation as the phone's history shows it: a
// prompt a person sent, a reply's text, or one line naming a tool call. A
// tool's output is left out: it can hold any file or secret the tool read.
type Entry struct {
	Kind string `firestore:"kind" json:"kind"`
	// Tool is the tool's name, for a tool call.
	Tool string    `firestore:"tool,omitempty" json:"tool,omitempty"`
	Text string    `firestore:"text" json:"text"`
	At   time.Time `firestore:"at" json:"at"`
}

// Snapshot is one session as published to the phone.
type Snapshot struct {
	ID        string `firestore:"id" json:"id"`
	Title     string `firestore:"title" json:"title"`
	Path      string `firestore:"path" json:"path"`
	AoeStatus string `firestore:"aoe_status" json:"aoe_status"`
	// Tool is the coding tool the session runs: "" for Claude Code, or
	// Antigravity.
	Tool string `firestore:"tool,omitempty" json:"tool,omitempty"`
	// Model is the model the worker is running on: a Claude Code model id or
	// alias (such as "claude-opus-5-5" or "sonnet"), or an Antigravity model
	// name or id.
	Model   string   `firestore:"model,omitempty" json:"model,omitempty"`
	State   State    `firestore:"state" json:"state"`
	Git     *Git     `firestore:"git" json:"git,omitempty"`
	Message *Message `firestore:"message" json:"message,omitempty"`
	// Prompt is the last thing a person sent the agent.
	Prompt *Message `firestore:"prompt" json:"prompt,omitempty"`
	// Cleared is true while the session's conversation is empty: just
	// started, or just cleared with /clear.
	Cleared bool `firestore:"cleared" json:"cleared"`
	// Conversation is the transcript id, and Latest the id of the newer of
	// Message and Prompt. A reply names both, so a reply the laptop picks up
	// late lands only where it was written for.
	Conversation string `firestore:"conversation" json:"conversation,omitempty"`
	Latest       string `firestore:"latest" json:"latest,omitempty"`
	// Prototypes are the pages the conversation published, newest first.
	Prototypes []Prototype `firestore:"prototypes" json:"prototypes,omitempty"`
	// Question is what the agent is asking with its multiple-choice dialog,
	// while that dialog is open.
	Question *Question `firestore:"question" json:"question,omitempty"`
	// Progress is what the agent is currently doing mid-turn, present only
	// while State is Working.
	Progress *Progress `firestore:"progress,omitempty" json:"progress,omitempty"`
	// Notified is true from a notification about the session until a device
	// shows the session. Swiping the notification away leaves it set: only
	// opening the worker means it was dealt with.
	Notified  bool      `firestore:"notified" json:"notified,omitempty"`
	UpdatedAt time.Time `firestore:"updated_at" json:"updated_at"`
	// Transcript is where the session's conversation is read from. It is
	// the laptop's business and is not published.
	Transcript Transcript `firestore:"-" json:"-"`
	// AoeHooks is aoe's hook directory for the session, set only when its
	// Claude Code profile does not run `peyk ping`. It is not published.
	AoeHooks string `firestore:"-" json:"-"`
	// Ideas are what an ideation run reported in Message, which then holds
	// the rest of the reply. They reach the phone as idea documents when
	// the run's task finishes, not on the snapshot.
	Ideas []idea.Draft `firestore:"-" json:"-"`
	// DefaultModel is the fallback model read from the session's launch
	// flags or profile settings, kept separate from the transcript so the
	// agent can prefer a worker's last known model over its launch flag
	// after /clear. It is not published.
	DefaultModel string `firestore:"-" json:"-"`
}

// Prototype is a page a session published with Claude Code's Artifact tool or
// `peyk publish`.
type Prototype struct {
	URL   string    `firestore:"url" json:"url"`
	Title string    `firestore:"title" json:"title"`
	Kind  string    `firestore:"kind" json:"kind"`
	At    time.Time `firestore:"at" json:"at"`
}

// A published page is a plain HTML page, a Design canvas, or another
// Artifact type. A task asks for HTML, Design, or Any, which leaves the
// choice between the two to the worker.
const (
	PrototypeHTML   = "html"
	PrototypeDesign = "design"
	PrototypeOther  = "other"
	PrototypeAny    = "any"
)

// Question is one open AskUserQuestion dialog. ID is the tool call's id, so
// an answer can name the dialog it was given for.
type Question struct {
	ID    string         `firestore:"id" json:"id"`
	At    time.Time      `firestore:"at" json:"at"`
	Items []QuestionItem `firestore:"items" json:"items"`
}

// QuestionItem is one tab of the dialog.
type QuestionItem struct {
	Question    string   `firestore:"question" json:"question"`
	Header      string   `firestore:"header" json:"header"`
	MultiSelect bool     `firestore:"multi_select" json:"multiSelect"`
	Options     []Option `firestore:"options" json:"options"`
}

// Option is one choice in a dialog tab.
type Option struct {
	Label       string `firestore:"label" json:"label"`
	Description string `firestore:"description" json:"description"`
}

// MaxPrototypes bounds the pages a snapshot lists, newest kept.
const MaxPrototypes = 10

// ClearCommand is the prompt recorded for a conversation started by /clear.
const ClearCommand = "/clear"

// SetExchange records the latest reply, prompt and model read from the
// transcript. A conversation is known to be empty only when its own
// transcript, rather than a guess at the directory's newest one, has no reply
// and no prompt other than the /clear that started it.
func (s *Snapshot) SetExchange(reply, prompt *Message, model string) {
	s.Ideas = nil
	if reply != nil {
		r := *reply
		r.Text, s.Ideas = idea.Split(r.Text)
		reply = &r
	}
	if model == "" && reply != nil {
		model = reply.Model
	}
	s.Message, s.Prompt, s.Model = reply, prompt, model
	s.Cleared = s.Transcript.ID != "" && reply == nil && (prompt == nil || prompt.Text == ClearCommand)
	s.Conversation = s.Transcript.ID
	s.Latest = ""
	switch {
	case reply != nil && (prompt == nil || !prompt.At.After(reply.At)):
		s.Latest = reply.ID
	case prompt != nil:
		s.Latest = prompt.ID
	}
}

// Transcript locates a session's conversation: for Claude Code, the
// directory of the working directory's transcripts and, once known, the
// transcript's own id; for Antigravity, the conversation's log directory and
// its id. Tool is "" for Claude Code.
type Transcript struct {
	Dir  string
	ID   string
	Tool string
	// AppDir and Workdir are where Antigravity keeps its conversations and
	// the directory it runs in, which together name its latest conversation.
	AppDir, Workdir string
}

// Antigravity is aoe's name for Antigravity CLI, the tool of a Transcript
// and a Snapshot that runs it.
const Antigravity = "antigravity"

// WithID returns t pointing at conversation id. Claude Code keeps every
// conversation for a directory in Dir; Antigravity gives each its own log
// directory under AppDir/brain/<id>.
func (t Transcript) WithID(id string) Transcript {
	t.ID = id
	if t.Tool == Antigravity && t.AppDir != "" {
		if id == "" {
			t.Dir = ""
		} else {
			t.Dir = filepath.Join(t.AppDir, "brain", id, ".system_generated", "logs")
		}
	}
	return t
}

// Derive turns aoe's status, whether the agent has tasks still running in the
// background, and the repository state into one board state. What the agent
// is doing wins over what it left behind, because a running agent is
// expected to have uncommitted work. aoe sees an agent between turns as idle
// even while its background tasks run; a stopped session's tasks died with it.
func Derive(aoeStatus string, backgrounded bool, git *Git) State {
	switch aoeStatus {
	case "waiting":
		return NeedsInput
	case "running", "starting", "creating":
		return Working
	case "error":
		return Error
	case "stopped":
	default:
		if backgrounded {
			return Working
		}
	}
	if git != nil {
		if git.Dirty > 0 {
			return Dirty
		}
		if git.Unpushed > 0 {
			return Unpushed
		}
	}
	if aoeStatus == "stopped" {
		return Stopped
	}
	return Idle
}

// Project is something a task can be sent to: a live aoe session, or a git
// repository under the projects root that would get a new session.
type Project struct {
	Name      string `firestore:"name" json:"name"`
	Path      string `firestore:"path" json:"path"`
	SessionID string `firestore:"session_id" json:"session_id"`
	// Free is false while the project's session is busy or has uncommitted
	// work; a task sent then would land on top of it. Unpushed commits do not
	// block a task, because pushing is batched with later work.
	Free bool `firestore:"free" json:"free"`
	// Busy says why a project is not free, in words the phone shows.
	Busy string `firestore:"busy" json:"busy"`
	// Active is the latest commit or reply, for ordering the picker.
	Active time.Time `firestore:"active" json:"active"`
	// Logo is the repository's own mark as a data URI, if it has one.
	Logo string `firestore:"logo,omitempty" json:"logo,omitempty"`
	// Hidden projects are left out of the phone's picker; the repository
	// and its session are untouched.
	Hidden bool `firestore:"hidden,omitempty" json:"hidden,omitempty"`
}

// Projects lists every session, plus every repository in repoDirs that no
// session works in, by name.
func Projects(snaps []Snapshot, repoDirs []string) []Project {
	var out []Project
	taken := map[string]bool{}
	for _, s := range snaps {
		taken[s.Path] = true
		p := Project{Name: s.Title, Path: s.Path, SessionID: s.ID, Free: true, Active: active(s)}
		switch s.State {
		case NeedsInput:
			p.Free, p.Busy = false, "needs input"
		case Error:
			p.Free, p.Busy = false, "session error"
		case Working:
			p.Free, p.Busy = false, "working"
		case Dirty:
			p.Free, p.Busy = false, "uncommitted changes"
		}
		out = append(out, p)
	}
	for _, dir := range repoDirs {
		if taken[dir] {
			continue
		}
		out = append(out, Project{Name: filepath.Base(dir), Path: dir, Free: true})
	}
	return out
}

func active(s Snapshot) time.Time {
	var t time.Time
	if s.Git != nil && s.Git.Last != nil {
		t = s.Git.Last.At
	}
	if s.Message != nil && s.Message.At.After(t) {
		t = s.Message.At
	}
	return t
}

// Finished says whether a session has finished work sent to it at sentAt: it
// has replied since, and stopped working without anything needing you. It
// returns the commit made for it, if any.
func Finished(s Snapshot, sentAt time.Time) (bool, string) {
	if s.Message == nil || !s.Message.At.After(sentAt) {
		return false, ""
	}
	switch s.State {
	case Idle, Unpushed, Stopped:
	default:
		return false, ""
	}
	if s.Git != nil && s.Git.Last != nil && s.Git.Last.At.After(sentAt) {
		return true, s.Git.Last.Hash
	}
	return true, ""
}

// Version is the laptop checkout's commit against the branch it follows, as
// of the last fetch, published as laptop/version.
type Version struct {
	Commit string `firestore:"commit" json:"commit"`
	// Behind counts the fetched commits HEAD does not have yet.
	Behind int `firestore:"behind" json:"behind"`
	// CheckedAt is when the checkout last fetched; zero if it never has.
	CheckedAt time.Time `firestore:"checked_at,omitempty" json:"checked_at,omitempty"`
}

// Model is one model a tool offers: its id as the tool takes it, and its
// name as the tool shows it.
type Model struct {
	ID   string `firestore:"id" json:"id"`
	Name string `firestore:"name" json:"name"`
}

// Models are what the laptop's tools can run, published as laptop/models.
// Claude Code's are its aliases, which the phone knows; Antigravity's depend
// on the account's plan, so they are listed.
type Models struct {
	Antigravity []Model `firestore:"antigravity" json:"antigravity"`
}
