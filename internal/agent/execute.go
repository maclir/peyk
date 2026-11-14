package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

// Actions are the things a command can make the laptop do.
type Actions interface {
	// Send delivers a prompt to a session, by id or title, starting it if stopped.
	Send(ctx context.Context, session, text string) error
	// Create adds a session for a repository that has none and returns its id.
	Create(ctx context.Context, dir, title string) (string, error)
	// Clear starts a session on a fresh conversation and returns once it
	// has one, so a prompt sent next lands in it.
	Clear(ctx context.Context, session string) error
	Stop(ctx context.Context, session string) error
	Push(ctx context.Context, dir string) error
}

// Commands is what the agent needs to accept commands. Without it the agent
// only publishes.
type Commands struct {
	Act    Actions
	Keys   func() ([]command.Key, error)
	Origin string
	RPID   string
}

// execute runs one command and records the outcome on its document. A
// refused or failed command is that command's result, not the agent's: only
// failing to record the outcome stops the agent.
func (a *Agent) execute(ctx context.Context, in store.Incoming) error {
	err := a.run(ctx, in)
	fields := map[string]any{"status": command.Done, "finished_at": a.Now()}
	if err != nil {
		fields["status"], fields["error"] = command.Failed, err.Error()
		a.Log.Printf("command %s (%s) failed: %v", in.ID, in.Command.Type, err)
	}
	return a.Store.Update(ctx, store.Commands, in.ID, fields)
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
		if !proj.Free {
			return fmt.Errorf("%s is not free: %s", proj.Name, proj.Busy)
		}
		id := proj.SessionID
		if id == "" {
			if id, err = act.Create(ctx, proj.Path, proj.Name); err != nil {
				return err
			}
		} else if p.Clear && !a.sessions[id].Cleared {
			if err := act.Clear(ctx, id); err != nil {
				return fmt.Errorf("clear %s: %w", proj.Name, err)
			}
		}
		sentAt := a.Now()
		if err := act.Send(ctx, id, p.Text); err != nil {
			return err
		}
		return a.Store.Update(ctx, store.Tasks, p.Task, map[string]any{
			"status": store.TaskDispatched, "dispatched_at": sentAt, "session_id": id,
		})
	case command.Reply:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		if p.Text == "" {
			return errors.New("reply needs text")
		}
		return act.Send(ctx, s.ID, p.Text)
	case command.Clear:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		switch s.State {
		case session.Working, session.NeedsInput:
			return fmt.Errorf("%s is %s; /clear would land in its turn", s.Title, s.State.Label())
		}
		if s.Cleared {
			return nil
		}
		return act.Clear(ctx, s.ID)
	case command.Stop:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		return act.Stop(ctx, s.ID)
	case command.Push:
		s, err := a.session(p.Session)
		if err != nil {
			return err
		}
		return act.Push(ctx, s.Path)
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
