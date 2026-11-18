// Command peyk is the laptop side of peyk.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/session"
)

const usage = `usage: peyk <command>

  status         print every live aoe session: state, last commit, push state, last reply
  agent          publish session state for the phone until stopped
  ping           tell a running agent that something changed (for hooks)
  usage          save the Claude rate limits from status line JSON on stdin (for statusLine)
  owner <email>  admit this Google account to peyk after its first sign-in
  import-tasks   add tasks from a JSON array on stdin: [{"project","text","created_at"}]
  pair <label>   trust a new passkey for signing commands
  unpair <label> stop trusting a passkey
  keys           list trusted passkeys
  install        run the agent and its menu bar app under launchd
  uninstall      stop both and remove them from launchd
  restart        rebuild and restart the launchd agent
  check-update   fetch this checkout and print how far behind it is
  update         fast-forward this checkout and restart the agent on it
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "peyk:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "status":
		snaps, err := collector().All(ctx)
		if err != nil {
			return err
		}
		printBoard(out, snaps, time.Now())
		return nil
	case "agent":
		return runAgent(ctx)
	case "ping":
		agent.Ping(socketPath())
		return nil
	case "usage":
		u, err := session.ParseStatusLine(os.Stdin)
		if err != nil || u == nil {
			return err
		}
		return laptop.SaveUsage(usagePath(), u)
	case "pair":
		if len(args) != 2 {
			return fmt.Errorf("usage: peyk pair <label>")
		}
		return pair(ctx, args[1], os.Stdin, out)
	case "unpair":
		if len(args) != 2 {
			return fmt.Errorf("usage: peyk unpair <label>")
		}
		return keyring().Remove(args[1])
	case "keys":
		return listKeys(out)
	case "install":
		return installJobs(ctx, out)
	case "uninstall":
		return uninstallJobs(ctx, out)
	case "restart":
		return restartAgent(ctx)
	case "check-update":
		return checkUpdate(ctx, out)
	case "update":
		return update(ctx)
	case "import-tasks":
		return importTasks(ctx, os.Stdin, out)
	case "owner":
		if len(args) != 2 {
			return fmt.Errorf("usage: peyk owner <email>")
		}
		return grantOwner(ctx, args[1])
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func collector() laptop.Collector {
	return laptop.Collector{
		Aoe:        laptop.Aoe{Profile: env("PEYK_AOE_PROFILE", "personal"), Run: laptop.Exec},
		Run:        laptop.Exec,
		ClaudeDirs: claudeDirs(),
		Root:       projectsRoot(),
		Now:        time.Now,
		Prototypes: &laptop.Prototypes{},
		Logos:      &laptop.Logos{Now: time.Now, TTL: 10 * time.Minute},
	}
}

// claudeDir follows Claude Code's own setting, so a separate personal profile
// is read where Claude Code writes it.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// claudeDirs lists the agent's own Claude Code directory, then the home
// directory's other .claude* profiles, which a project's .envrc can select.
func claudeDirs() []string {
	own := claudeDir()
	dirs := []string{own}
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".claude*"))
	for _, m := range matches {
		if info, err := os.Stat(filepath.Join(m, "projects")); err == nil && info.IsDir() && m != own {
			dirs = append(dirs, m)
		}
	}
	return dirs
}

// claudeConfig is Claude Code's global config file: inside CLAUDE_CONFIG_DIR
// when that is set, otherwise beside the home directory's .claude.
func claudeConfig() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// projectsRoot is where repositories without a session live, offered in the
// phone's project picker: by default the directory that holds this checkout,
// which launchd and make run from.
func projectsRoot() string {
	if d := os.Getenv("PEYK_PROJECTS_ROOT"); d != "" {
		return d
	}
	wd, _ := os.Getwd()
	return filepath.Dir(wd)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var stateLabels = map[session.State]string{
	session.NeedsInput: "needs input",
	session.Error:      "error",
	session.Dirty:      "uncommitted",
	session.Unpushed:   "unpushed",
	session.Working:    "working",
	session.Idle:       "idle",
	session.Stopped:    "stopped",
}

func printBoard(out io.Writer, snaps []session.Snapshot, now time.Time) {
	if len(snaps) == 0 {
		fmt.Fprintln(out, "No live Claude Code sessions.")
		return
	}
	for _, s := range snaps {
		fmt.Fprintf(out, "%-14s %-12s %s\n", s.Title, stateLabels[s.State], gitLine(s.Git, now))
		if s.Message != nil {
			fmt.Fprintf(out, "%14s %s\n", "", oneLine(s.Message.Text, 100))
		}
	}
}

func gitLine(g *session.Git, now time.Time) string {
	if g == nil {
		return "no repository"
	}
	var parts []string
	if g.Last != nil {
		parts = append(parts, fmt.Sprintf("%s %s (%s)", g.Last.Hash, oneLine(g.Last.Subject, 50), ago(now.Sub(g.Last.At))))
	}
	switch {
	case g.Upstream == "" && g.Unpushed > 0:
		parts = append(parts, fmt.Sprintf("%d unpushed, no remote branch", g.Unpushed))
	case g.Unpushed > 0:
		parts = append(parts, fmt.Sprintf("%d unpushed", g.Unpushed))
	case g.Upstream != "":
		parts = append(parts, "pushed")
	}
	if g.Dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d changed files", g.Dirty))
	}
	return strings.Join(parts, " · ")
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
