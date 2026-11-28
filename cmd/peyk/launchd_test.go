package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/agent"
)

func TestRestartHoldsSleepAssertionUntilReplacementIsUp(t *testing.T) {
	start := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	var events []string
	polls := 0
	r := restarter{
		stayAwake: func() func() {
			events = append(events, "acquire")
			return func() { events = append(events, "release") }
		},
		run: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			events = append(events, name+" "+args[0]+" "+args[1])
			return []byte("service spawned with pid: 200\n"), nil
		},
		health: func() (agent.Health, error) {
			polls++
			if polls <= 2 {
				return agent.Health{PID: 100, StartedAt: start}, nil
			}
			events = append(events, "ready")
			return agent.Health{PID: 201, StartedAt: start.Add(time.Second)}, nil
		},
		alive: func(pid int) bool { return pid == 100 || pid == 200 || pid == 201 },
		wait:  time.Second,
		poll:  time.Millisecond,
	}
	if err := r.restart(context.Background(), "gui/501/com.peyk.agent"); err != nil {
		t.Fatal(err)
	}
	want := []string{"acquire", "launchctl kickstart -kp", "ready", "release"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRestartStopsWaitingIfSpawnedJobExits(t *testing.T) {
	released := false
	checks := 0
	r := restarter{
		stayAwake: func() func() { return func() { released = true } },
		run: func(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
			return []byte("service spawned with pid: 300\n"), nil
		},
		health: func() (agent.Health, error) { return agent.Health{}, errors.New("missing") },
		alive: func(pid int) bool {
			if pid == 300 {
				checks++
				return checks < 2
			}
			return false
		},
		wait: 5 * time.Second,
		poll: time.Millisecond,
	}
	start := time.Now()
	if err := r.restart(context.Background(), "gui/501/com.peyk.agent"); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("sleep assertion was not released")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("waited %v after spawned job exited", time.Since(start))
	}
}

func TestRestartReleasesAssertionOnKickstartError(t *testing.T) {
	released := false
	r := restarter{
		stayAwake: func() func() { return func() { released = true } },
		run: func(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("kickstart failed")
		},
		health: func() (agent.Health, error) { return agent.Health{}, errors.New("missing") },
		alive:  func(int) bool { return false },
		wait:   time.Second,
		poll:   time.Millisecond,
	}
	if err := r.restart(context.Background(), "gui/501/com.peyk.agent"); err == nil {
		t.Fatal("want error")
	}
	if !released {
		t.Fatal("sleep assertion was not released")
	}
}

func TestParseSpawnedPID(t *testing.T) {
	if got := parseSpawnedPID([]byte("service spawned with pid: 4219\n")); got != 4219 {
		t.Fatalf("got %d, want 4219", got)
	}
	if got := parseSpawnedPID([]byte("no pid here\n")); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}
