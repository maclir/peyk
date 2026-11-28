package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/idea"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

// Actions are the things a command can make the laptop do.
type Actions interface {
	// Send delivers a prompt to a session, by id or title, starting it if
	// stopped, and verifies that non-slash prompts land in t's transcript.
	Send(ctx context.Context, session string, t session.Transcript, text string) error
	// Create adds a session for a repository that has none and returns its id.
	Create(ctx context.Context, dir, title string) (string, error)
	// Clear starts a session on a fresh conversation, which Claude Code
	// records in t's directory, and returns its transcript id once it has
	// one, so a prompt sent next lands in it.
	Clear(ctx context.Context, session string, t session.Transcript) (string, error)
	Stop(ctx context.Context, session string) error
	Start(ctx context.Context, session string) error
	Restart(ctx context.Context, session string) error
	// Resume relaunches a session on conversation, an earlier transcript in
	// t's directory, starting it if stopped.
	Resume(ctx context.Context, session string, t session.Transcript, conversation string, stopped bool) error
	Push(ctx context.Context, dir string) error
	// Swap replaces a session with one in the same directory that runs tool
	// on model with a fresh conversation, stops the old session, and returns
	// the new session's id.
	Swap(ctx context.Context, s session.Snapshot, tool, model string) (string, error)
	// Answer closes a session's open dialog for question and sends text as
	// the answer.
	Answer(ctx context.Context, session string, t session.Transcript, question, text string) error
}

// Commands is what the agent needs to accept commands. Without it the agent
// only publishes.
type Commands struct {
	Act    Actions
	Keys   func() ([]command.Key, error)
	Origin string
	RPID   string
	// Attachments is the directory a dispatch's images are saved to.
	Attachments string
	// Peyk is the path to the peyk binary named in prototype prompts for
	// non-Claude workers; defaults to "peyk".
	Peyk string
}

// execute runs one command and records the outcome on its document. A
// refused or failed command is that command's result, not the agent's: only
// failing to record the outcome stops the agent.
func (a *Agent) execute(ctx context.Context, in store.Incoming) error {
	err := a.run(ctx, in)
	if errors.Is(err, errHeld) {
		return a.Store.Update(ctx, store.Commands, in.ID, map[string]any{"status": command.Held})
	}
	fields := map[string]any{"status": command.Done, "finished_at": a.Now()}
	if err != nil {
		fields["status"], fields["error"] = command.Failed, err.Error()
		a.Log.Printf("command %s (%s) failed: %v", in.ID, in.Command.Type, err)
	}
	if err := a.Store.Update(ctx, store.Commands, in.ID, fields); err != nil {
		return err
	}
	// A session the command added or replaced is itself the event: no hook
	// reports it before the next command, which would act on the old one.
	if a.sessionsChanged {
		a.sessionsChanged = false
		return a.Refresh(ctx)
	}
	return nil
}

func (a *Agent) run(ctx context.Context, in store.Incoming) error {
	keys, err := a.Commands.Keys()
	if err != nil {
		return fmt.Errorf("read paired keys: %w", err)
	}
	p, err := command.Check(in.ID, in.Command, keys, a.Commands.Origin, a.Commands.RPID, a.Now())
	if err != nil {
		return err
	}
	// Acting on a session is seeing it, even from a device not showing it.
	if p.Session != "" {
		defer a.seen(ctx, p.Session)
	}
	act := a.Commands.Act
	switch p.Type {
	case command.Dispatch:
		if p.Task == "" || p.Text == "" {
			return errors.New("dispatch needs a task and its text")
		}
		proj, ok := a.projects[p.Project]
		if !ok {
			return fmt.Errorf("no project named %s", p.Project)
		}
		if proj.Hidden {
			return fmt.Errorf("%s is hidden; show it first", proj.Name)
		}
		if !proj.Free {
			return fmt.Errorf("%s is not free: %s", proj.Name, proj.Busy)
		}
		text, err := prototypeText(p.Text, p.Prototype, a.sessions[proj.SessionID].Tool, a.Commands.Peyk)
		if err != nil {
			return err
		}
		if p.Ideate != nil {
			if p.Prototype != "" {
				return errors.New("a task asks for a prototype or for ideas, not both")
			}
			known, err := a.ideas(ctx, proj.Name)
			if err != nil {
				return err
			}
			if text, err = idea.Prompt(p.Text, *p.Ideate, known, a.projectLenses(proj)...); err != nil {
				return err
			}
		}
		if text, err = a.attach(ctx, text, p.Images); err != nil {
			return err
		}
		id := proj.SessionID
		conversation := a.sessions[id].Transcript.ID
		if id == "" {
			if id, err = act.Create(ctx, proj.Path, proj.Name); err != nil {
				return err
			}
			a.sessionsChanged = true
		} else if p.Clear && !a.sessions[id].Cleared {
			t, err := act.Clear(ctx, id, a.sessions[id].Transcript)
			if err != nil {
				return fmt.Errorf("clear %s: %w", proj.Name, err)
			}
			if err := a.cleared(ctx, id, t); err != nil {
				return err
			}
			conversation = t
		}
		sentAt := a.Now()
		if err := act.Send(ctx, id, a.sessions[id].Transcript, text); err != nil {
			return err
		}
		fields := map[string]any{"status": store.TaskDispatched, "dispatched_at": sentAt, "session_id": id}
		// A new session has no conversation until Claude Code starts; the
		// task takes it when it finishes.
		if conversation != "" {
			fields["conversation_id"] = conversation
		}
		return a.Store.Update(ctx, store.Tasks, p.Task, fields)
	case command.Reply:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if p.Text == "" && len(p.Images) == 0 {
			return errors.New("reply needs text or an image")
		}
		if p.Late(a.Now()) {
			if err := lateReply(s, p); err != nil {
				return err
			}
		}
		text, err := a.attach(ctx, p.Text, p.Images)
		if err != nil {
			return err
		}
		return act.Send(ctx, s.ID, s.Transcript, text)
	case command.Answer:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if p.Text == "" || p.Question == "" {
			return errors.New("answer needs the question and its text")
		}
		if s.Question == nil || s.Question.ID != p.Question {
			return fmt.Errorf("%s is no longer asking that question", s.Title)
		}
		return act.Answer(ctx, s.ID, s.Transcript, p.Question, p.Text)
	case command.Clear:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		return a.clear(ctx, act, s)
	case command.Stop:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if p.Clear {
			if err := a.clear(ctx, act, s); err != nil {
				return err
			}
		}
		return act.Stop(ctx, s.ID)
	case command.Start:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if s.AoeStatus != "stopped" {
			return fmt.Errorf("%s is already running", s.Title)
		}
		return act.Start(ctx, s.ID)
	case command.Restart:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if s.AoeStatus == "stopped" {
			return fmt.Errorf("%s is stopped; start it instead", s.Title)
		}
		return act.Restart(ctx, s.ID)
	case command.Resume:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if p.Conversation == "" {
			return errors.New("resume needs a conversation")
		}
		// A worker that is not free would lose its turn or its uncommitted
		// work's conversation.
		if proj := a.projects[s.Title]; !proj.Free {
			return fmt.Errorf("%s is not free: %s", s.Title, proj.Busy)
		}
		if s.Transcript.ID == p.Conversation {
			return nil
		}
		if err := act.Resume(ctx, s.ID, s.Transcript, p.Conversation, s.AoeStatus == "stopped"); err != nil {
			return err
		}
		return a.cleared(ctx, s.ID, p.Conversation)
	case command.Push:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		return act.Push(ctx, s.Path)
	case command.Hide, command.Show:
		return a.setHidden(ctx, p.Projects, p.Type == command.Hide)
	case command.Model:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		return a.switchModel(ctx, act, s, p.Text, p.Clear)
	case command.CheckUpdate, command.Update:
		if a.Checkout == nil {
			return errors.New("this agent cannot update itself")
		}
		if err := a.Checkout.Fetch(ctx); err != nil {
			return err
		}
		if p.Type == command.Update {
			if err := a.Checkout.Update(ctx); err != nil {
				return err
			}
			a.restart = true
		}
		return a.publishVersion(ctx)
	}
	return fmt.Errorf("unknown command type %q", p.Type)
}

func (a *Agent) session(id string) (session.Snapshot, error) {
	s, ok := a.sessions[id]
	if !ok {
		return s, fmt.Errorf("no session %s", id)
	}
	return s, nil
}

// errHeld leaves a reply for the phone to send again or cancel.
var errHeld = errors.New("the conversation moved on since this reply was written")

// errConversationGone fails a reply whose conversation was cleared or
// replaced: it was written for a context the session no longer has.
var errConversationGone = errors.New("the conversation was cleared before the laptop could send this reply")

// lateReply says whether a reply that waited for the laptop still fits its
// session: the same conversation, still at the message it answers.
func lateReply(s session.Snapshot, p command.Payload) error {
	if s.Transcript.ID != p.Conversation {
		return errConversationGone
	}
	if s.Latest != p.After {
		return errHeld
	}
	return nil
}

// dropHeld fails the held replies whose session or conversation is gone,
// so a clear empties the session's reply queue.
func (a *Agent) dropHeld(ctx context.Context) error {
	hr, ok := a.Store.(store.HeldReader)
	if !ok {
		return nil
	}
	held, err := hr.HeldReplies(ctx)
	if err != nil {
		return err
	}
	for id, c := range held {
		var p command.Payload
		if json.Unmarshal([]byte(c.Payload), &p) == nil {
			if s, ok := a.sessions[p.Session]; ok && s.Transcript.ID == p.Conversation {
				continue
			}
		}
		if err := a.Store.Update(ctx, store.Commands, id, map[string]any{
			"status": command.Failed, "error": errConversationGone.Error(), "finished_at": a.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// clear starts an idle session on a fresh conversation. A session that is
// working or asking would read /clear as part of its turn, so it is refused.
func (a *Agent) clear(ctx context.Context, act Actions, s session.Snapshot) error {
	switch s.State {
	case session.Working, session.NeedsInput:
		return fmt.Errorf("%s is %s; /clear would land in its turn", s.Title, s.State.Label())
	}
	if s.Cleared {
		return nil
	}
	t, err := act.Clear(ctx, s.ID, s.Transcript)
	if err != nil {
		return err
	}
	return a.cleared(ctx, s.ID, t)
}

// claudeModels are the aliases Claude Code resolves to the newest model of
// each family, so the phone never needs a version to name one.
var claudeModels = map[string]bool{"fable": true, "opus": true, "sonnet": true, "haiku": true}

// toolFor is the tool that runs model: "" for Claude Code.
func (a *Agent) toolFor(model string) (string, error) {
	switch {
	case claudeModels[model]:
		return "", nil
	case a.models[model]:
		return session.Antigravity, nil
	}
	return "", fmt.Errorf("no model named %q", model)
}

// switchModel changes a session's model. Active turns and open dialogs are
// refused because /model, /clear, or stopping the old session would land in
// the turn. Uncommitted changes are allowed: both tools share the working
// directory on disk.
//
// When clear is true (or the session is already cleared), the worker starts
// on an empty conversation — /clear then /model on the same tool, or a fresh
// started session on the other tool with no catch-up prompt. Otherwise the
// current conversation is kept in place on the same tool, or handed to a fresh
// session on the other tool via catchUp.
func (a *Agent) switchModel(ctx context.Context, act Actions, s session.Snapshot, model string, clear bool) error {
	tool, err := a.toolFor(model)
	if err != nil {
		return err
	}
	switch s.State {
	case session.Working, session.NeedsInput:
		if tool == s.Tool {
			return fmt.Errorf("%s is %s; /model would land in its turn", s.Title, s.State.Label())
		}
		return fmt.Errorf("%s is %s; wait for it to finish before switching tools", s.Title, s.State.Label())
	}
	if tool == s.Tool {
		if clear {
			if err := a.clear(ctx, act, s); err != nil {
				return err
			}
			s = a.sessions[s.ID]
		}
		if err := act.Send(ctx, s.ID, s.Transcript, "/model "+model); err != nil {
			return err
		}
		a.recordModelSwitch(s.ID, model, s)
		s.Model = model
		a.sessions[s.ID] = s
		return a.publishSessions(ctx)
	}
	id, err := act.Swap(ctx, s, tool, model)
	if id != "" {
		a.sessionsChanged = true
		a.recordModelSwitch(id, model, session.Snapshot{})
	}
	if err != nil {
		return err
	}
	if clear || s.Cleared {
		return act.Start(ctx, id)
	}
	if err := a.reattachTasks(ctx, s.ID, id); err != nil {
		return err
	}
	return act.Send(ctx, id, session.Transcript{}, catchUp(s))
}

// reattachTasks points dispatched tasks carried across a tool swap at the new
// session, clearing conversation_id so finishTasks records the new worker's
// conversation once it finishes.
func (a *Agent) reattachTasks(ctx context.Context, from, to string) error {
	tr, ok := a.Store.(store.TaskReader)
	if !ok {
		return nil
	}
	tasks, err := tr.DispatchedTasks(ctx)
	if err != nil {
		return err
	}
	for id, t := range tasks {
		if t.SessionID != from {
			continue
		}
		if err := a.Store.Update(ctx, store.Tasks, id, map[string]any{"session_id": to, "conversation_id": ""}); err != nil {
			return err
		}
	}
	return nil
}

// catchUp is the first prompt to a worker moved to another tool. It points
// at the old conversation rather than copying it: the new tool reads what
// it needs, and nothing is lost in a summary written for it.
func catchUp(from session.Snapshot) string {
	tool, log := "Claude Code", ""
	if from.Tool == session.Antigravity {
		tool = "Antigravity"
		if from.Transcript.Dir != "" {
			log = from.Transcript.Dir + "/transcript.jsonl"
		}
	} else if from.Transcript.ID != "" {
		log = from.Transcript.Dir + "/" + from.Transcript.ID + ".jsonl"
	}
	if log == "" {
		return "This project's worker just moved here from " + tool + ", which had no conversation yet. Read the repository's state and wait for the next request."
	}
	return "This project's worker just moved here from " + tool + ". Before anything else, read that conversation, newest first, as far back as you need to know where the work stands: " + log +
		"\n\nIt is a JSON Lines transcript and can be large; read it in parts. Then reply with a few lines on where things stand and what was asked last, and wait for the next request. Do not change files until asked."
}
