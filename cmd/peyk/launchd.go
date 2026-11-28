package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/launchd"
)

// jobs are the agent, run through make so a restart rebuilds it and reads
// .env afresh, and the menu bar app that shows and controls it.
func jobs(repo, home string, lookup func(string) (string, bool)) []launchd.Job {
	menuEnv := map[string]string{"PEYK_REPO": repo}
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
	r := defaultRestarter()
	before := r.currentHealth()
	release := r.stayAwake()
	defer release()
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
	r.waitForReplacement(ctx, before, 0)
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

// restartAgent rebuilds and restarts the agent while holding a sleep assertion
// so macOS does not enter Idle Sleep on battery during the restart gap.
func restartAgent(ctx context.Context) error {
	return defaultRestarter().restart(ctx, launchd.Domain()+"/"+launchd.Agent)
}

type restarter struct {
	run       laptop.Runner
	stayAwake func() func()
	health    func() (agent.Health, error)
	alive     func(int) bool
	wait      time.Duration
	poll      time.Duration
}

func defaultRestarter() restarter {
	return restarter{
		run:       laptop.Exec,
		stayAwake: agent.StayAwake,
		health:    func() (agent.Health, error) { return agent.ReadHealth(healthPath()) },
		alive:     pidAlive,
		wait:      30 * time.Second,
		poll:      200 * time.Millisecond,
	}
}

func (r restarter) currentHealth() agent.Health {
	h, err := r.health()
	if err != nil || !r.alive(h.PID) {
		return agent.Health{}
	}
	return h
}

func (r restarter) restart(ctx context.Context, target string) error {
	before := r.currentHealth()
	release := r.stayAwake()
	defer release()
	out, err := r.run(ctx, "", "launchctl", "kickstart", "-kp", target)
	if err != nil {
		return err
	}
	r.waitForReplacement(ctx, before, parseSpawnedPID(out))
	return nil
}

// waitForReplacement holds the caller's sleep assertion until the replacement
// agent writes a new live health file (at which point its own caffeinate is
// already running), the spawned make job exits early, or the wait expires.
func (r restarter) waitForReplacement(ctx context.Context, before agent.Health, spawned int) {
	if r.isReady(before) || (spawned > 0 && !r.alive(spawned)) {
		return
	}
	deadline := time.NewTimer(r.wait)
	defer deadline.Stop()
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			if r.isReady(before) || (spawned > 0 && !r.alive(spawned)) {
				return
			}
		}
	}
}

func (r restarter) isReady(before agent.Health) bool {
	h, err := r.health()
	if err != nil || h.PID <= 0 {
		return false
	}
	if h.PID == before.PID && h.StartedAt.Equal(before.StartedAt) {
		return false
	}
	return r.alive(h.PID)
}

func parseSpawnedPID(out []byte) int {
	const prefix = "service spawned with pid:"
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, prefix); i >= 0 {
			if pid, err := strconv.Atoi(strings.TrimSpace(line[i+len(prefix):])); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
