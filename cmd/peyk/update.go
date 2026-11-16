package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/launchd"
)

// checkout is the agent's own checkout. The agent restarts itself after
// an update, which rebuilds it; the menu bar app is a separate build and
// process, so a change to it is rebuilt and relaunched here.
type checkout struct {
	laptop.Checkout
}

func ownCheckout() (checkout, error) {
	dir, err := os.Getwd()
	return checkout{laptop.Checkout{Dir: dir, Run: laptop.Exec}}, err
}

func menuChanged(paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, "menubar/") {
			return true
		}
	}
	return false
}

func (c checkout) rebuildMenu(ctx context.Context) error {
	_, err := laptop.Exec(ctx, c.Dir, "make", "-s", "menu")
	return err
}

func relaunchMenu(ctx context.Context) {
	_, _ = laptop.Exec(ctx, "", "launchctl", "kickstart", "-k", launchd.Domain()+"/"+launchd.Menu)
}

// Update is the agent's: it rebuilds the menu bar app if it changed, and the
// agent exits afterwards so launchd starts the new agent.
func (c checkout) Update(ctx context.Context) error {
	changed, err := c.Checkout.Update(ctx)
	if err != nil || !menuChanged(changed) {
		return err
	}
	if err := c.rebuildMenu(ctx); err != nil {
		return err
	}
	relaunchMenu(ctx)
	return nil
}

var _ agent.Checkout = checkout{}

// checkUpdate fetches, prints the version as JSON for the menu bar app, and
// pings the agent so the phone sees it too.
func checkUpdate(ctx context.Context, out io.Writer) error {
	c, err := ownCheckout()
	if err != nil {
		return err
	}
	if err := c.Fetch(ctx); err != nil {
		return err
	}
	v, err := c.Version(ctx)
	if err != nil {
		return err
	}
	agent.Ping(socketPath())
	return json.NewEncoder(out).Encode(v)
}

// update is the menu bar app's: the app runs it, so the app is relaunched
// last, after the agent has been restarted.
func update(ctx context.Context) error {
	c, err := ownCheckout()
	if err != nil {
		return err
	}
	if err := c.Fetch(ctx); err != nil {
		return err
	}
	changed, err := c.Checkout.Update(ctx)
	if err != nil {
		return err
	}
	rebuilt := menuChanged(changed)
	if rebuilt {
		if err := c.rebuildMenu(ctx); err != nil {
			return err
		}
	}
	if err := restartAgent(ctx); err != nil {
		return err
	}
	if rebuilt {
		relaunchMenu(ctx)
	}
	return nil
}
