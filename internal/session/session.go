// Package session holds what peyk knows about one aoe coding session and how
// its state is derived. It does no I/O.
package session

import (
	"path/filepath"
	"time"
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
	for i, v := range []State{NeedsInput, Error, Dirty, Unpushed, Working, Idle, Stopped} {
		if v == s {
			return i
		}
	}
	return 99
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
}

// Snapshot is one session as published to the phone.
type Snapshot struct {
	ID        string   `firestore:"id" json:"id"`
	Title     string   `firestore:"title" json:"title"`
	Path      string   `firestore:"path" json:"path"`
	AoeStatus string   `firestore:"aoe_status" json:"aoe_status"`
	State     State    `firestore:"state" json:"state"`
	Git       *Git     `firestore:"git" json:"git,omitempty"`
	Message   *Message `firestore:"message" json:"message,omitempty"`
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
	Question  *Question `firestore:"question" json:"question,omitempty"`
	UpdatedAt time.Time `firestore:"updated_at" json:"updated_at"`
	// Transcript is where the session's conversation is read from. It is
	// the laptop's business and is not published.
	Transcript Transcript `firestore:"-" json:"-"`
}

// Prototype is a page a session published with Claude Code's Artifact tool.
type Prototype struct {
	URL   string    `firestore:"url" json:"url"`
	Title string    `firestore:"title" json:"title"`
	At    time.Time `firestore:"at" json:"at"`
}

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

// SetExchange records the latest reply and prompt read from the transcript.
// A conversation is known to be empty only when its own transcript, rather
// than a guess at the directory's newest one, has no reply and no prompt
// other than the /clear that started it.
func (s *Snapshot) SetExchange(reply, prompt *Message) {
	s.Message, s.Prompt = reply, prompt
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

// Transcript locates a Claude Code transcript: the directory for the
// session's working directory and, once known, the transcript's own id.
type Transcript struct {
	Dir string
	ID  string
}

// Derive turns aoe's status and the repository state into one board state.
// What the agent is doing wins over what it left behind, because a running
// agent is expected to have uncommitted work.
func Derive(aoeStatus string, git *Git) State {
	switch aoeStatus {
	case "waiting":
		return NeedsInput
	case "running", "starting", "creating":
		return Working
	case "error":
		return Error
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
