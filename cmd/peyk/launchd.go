package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/launchd"
)

// jobs are the agent, run through make so a restart rebuilds it and reads
// .env afresh, and the menu bar app that shows and controls it.
func jobs(repo, home string, lookup func(string) (string, bool)) []launchd.Job {
	menuEnv := map[string]string{}
	if o, ok := lookup("PEYK_ORIGIN"); ok && o != "" {
		menuEnv["PEYK_ORIGIN"] = o
	}
	return []launchd.Job{
		{
			Label:     launchd.Agent,
			Program:   []string{"/usr/bin/make", "-s", "serve"},
			Dir:       repo,
			Env:       launchd.Capture(lookup),
			Log:       filepath.Join(home, ".peyk", "agent.log"),
			KeepAlive: true,
		},
		{
			Label:   launchd.Menu,
			Program: []string{filepath.Join(repo, "bin", "Peyk.app", "Contents", "MacOS", "Peyk")},
			Env:     menuEnv,
			Log:     filepath.Join(home, ".peyk", "menu.log"),
		},
	}
}

func installJobs(ctx context.Context, out io.Writer) error {
	repo, err := os.Getwd()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	js := jobs(repo, home, os.LookupEnv)
	if _, err := os.Stat(js[1].Program[0]); err != nil {
		return fmt.Errorf("menu bar app not built; run make install-agent: %w", err)
	}
	for _, d := range []string{filepath.Join(home, ".peyk"), filepath.Dir(launchd.Path(home, launchd.Agent))} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	for _, j := range js {
		path := launchd.Path(home, j.Label)
		if err := os.WriteFile(path, j.Plist(), 0o644); err != nil {
			return err
		}
		_, _ = laptop.Exec(ctx, "", "launchctl", "bootout", launchd.Domain()+"/"+j.Label)
		if err := bootstrap(ctx, path); err != nil {
			return fmt.Errorf("start %s: %w", j.Label, err)
		}
		fmt.Fprintf(out, "started %s (%s)\n", j.Label, path)
	}
	return nil
}

// bootstrap retries because a job just booted out can take a moment to stop,
// and launchd refuses to load it again until it has.
func bootstrap(ctx context.Context, path string) error {
	var err error
	for range 10 {
		if _, err = laptop.Exec(ctx, "", "launchctl", "bootstrap", launchd.Domain(), path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return err
}

func uninstallJobs(ctx context.Context, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, label := range []string{launchd.Menu, launchd.Agent} {
		_, _ = laptop.Exec(ctx, "", "launchctl", "bootout", launchd.Domain()+"/"+label)
		if err := os.Remove(launchd.Path(home, label)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintf(out, "removed %s\n", label)
	}
	return nil
}
