// Package session holds what peyk knows about one aoe coding session and how
// its state is derived. It does no I/O.
package session

import "time"

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
