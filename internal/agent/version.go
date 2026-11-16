package agent

import (
	"context"

	"github.com/maclir/peyk/internal/session"
)

// Checkout is the agent's own checkout, which the phone can update.
type Checkout interface {
	Fetch(ctx context.Context) error
	Version(ctx context.Context) (session.Version, error)
	// Update fast-forwards to the fetched upstream and rebuilds what must be
	// rebuilt outside the agent; the agent itself restarts afterwards.
	Update(ctx context.Context) error
}

// publishVersion writes laptop/version when it changed. A checkout git cannot
// read (no upstream, say) is logged and left unpublished rather than
// stopping the agent, which can publish everything else.
func (a *Agent) publishVersion(ctx context.Context) error {
	if a.Checkout == nil {
		return nil
	}
	v, err := a.Checkout.Version(ctx)
	if err != nil {
		a.Log.Printf("version: %v", err)
		return nil
	}
	key := contentKey(v)
	if key == a.version {
		return nil
	}
	if err := a.Store.Put(ctx, "laptop", "version", v); err != nil {
		return err
	}
	a.version = key
	return nil
}
