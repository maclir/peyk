// Package session holds what peyk knows about one aoe coding session and how
// its state is derived. It does no I/O.
package session

import (
	"path/filepath"
	"strconv"
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
	Upstream string  `firestore:"upstream" json:"upstream"`
	Unpushed int     `firestore:"unpushed" json:"unpushed"`
	Dirty    int     `firestore:"dirty" json:"dirty"`
	Last     *Commit `firestore:"last" json:"last,omitempty"`
}

// Message is the agent's latest reply, kept as the Markdown it wrote.
type Message struct {
	Text string    `firestore:"text" json:"text"`
	At   time.Time `firestore:"at" json:"at"`
}

// Snapshot is one session as published to the phone.
type Snapshot struct {
	ID        string    `firestore:"id" json:"id"`
	Title     string    `firestore:"title" json:"title"`
	Path      string    `firestore:"path" json:"path"`
	AoeStatus string    `firestore:"aoe_status" json:"aoe_status"`
	State     State     `firestore:"state" json:"state"`
	Git       *Git      `firestore:"git" json:"git,omitempty"`
	Message   *Message  `firestore:"message" json:"message,omitempty"`
	UpdatedAt time.Time `firestore:"updated_at" json:"updated_at"`
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
	// Free is false while the project's session is busy or has work you have
	// not dealt with; a task sent then would land on top of it.
	Free bool `firestore:"free" json:"free"`
	// Busy says why a project is not free, in words the phone shows.
	Busy string `firestore:"busy" json:"busy"`
	// Active is the latest commit or reply, for ordering the picker.
	Active time.Time `firestore:"active" json:"active"`
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
		case Unpushed:
			p.Free, p.Busy = false, plural(s.Git.Unpushed, "unpushed commit")
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

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
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
