package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/inbox"
	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/notify"
	"github.com/maclir/peyk/internal/owner"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

func socketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peyk", "agent.sock")
}

// usagePath is where `peyk usage` saves the rate limits; it sits in a
// directory of its own so the agent's watcher sees nothing else.
func usagePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peyk", "usage", "claude.json")
}

func attachmentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peyk", "attachments")
}

func project() (string, error) {
	p := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if p == "" {
		return "", fmt.Errorf("GOOGLE_CLOUD_PROJECT is not set; run through make, which reads .env")
	}
	return p, nil
}

// openStore connects as the peyk-agent service account, impersonated from
// your own login. The token it holds can reach this project's Firestore and
// nothing else, and no key file exists to leak.
func openStore(ctx context.Context) (*store.Firestore, string, error) {
	p, err := project()
	if err != nil {
		return nil, "", err
	}
	ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
		TargetPrincipal: env("PEYK_AGENT_ACCOUNT", "peyk-agent@"+p+".iam.gserviceaccount.com"),
		Scopes:          []string{"https://www.googleapis.com/auth/datastore"},
	})
	if err != nil {
		return nil, "", fmt.Errorf("impersonate agent account: %w", err)
	}
	fs, err := store.NewFirestore(ctx, p, option.WithTokenSource(ts))
	return fs, p, err
}

func runAgent(ctx context.Context) error {
	o, err := origin()
	if err != nil {
		return err
	}
	fs, p, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()

	ln, err := agent.Listen(socketPath())
	if err != nil {
		return err
	}
	watcher, err := agent.NewGitWatcher(laptop.Exec)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pings := make(chan struct{}, 1)
	go agent.Serve(ctx, ln, pings)
	// Either background listener failing ends the agent; it says which.
	bgErr := make(chan error, 2)
	background := func(f func() error) {
		go func() {
			err := f()
			bgErr <- err
			if err != nil {
				cancel()
			}
		}()
	}
	background(func() error { return watcher.Forward(ctx, pings) })
	cmds := make(chan store.Incoming)
	background(func() error { return fs.Commands(ctx, cmds) })

	logger := log.New(os.Stderr, "peyk: ", log.LstdFlags)
	logger.Printf("publishing to %s; Ctrl-C to go offline", p)
	keys, err := vapidKeys()
	if err != nil {
		return err
	}
	if err := fs.Put(ctx, "laptop", "push", map[string]string{"vapid_public_key": keys.Public}); err != nil {
		return err
	}
	edits := agent.NewEditWatcher(pings)
	defer edits.Close()
	replies := make(chan struct{}, 1)
	transcripts := agent.NewEditWatcher(replies)
	defer transcripts.Close()
	usage := make(chan struct{}, 1)
	usageWatch := agent.NewEditWatcher(usage)
	defer usageWatch.Close()
	if err := os.MkdirAll(filepath.Dir(usagePath()), 0o700); err != nil {
		return err
	}
	if err := usageWatch.Set(ctx, []string{filepath.Dir(usagePath())}); err != nil {
		return err
	}
	c := collector()
	a := &agent.Agent{
		Edits:       edits,
		Transcripts: transcripts, Replies: replies,
		Notify:  notifier{store: fs, sender: notify.Sender{Keys: keys, Subscriber: o.String()}},
		Collect: c, Store: fs, Watch: watcher, Now: time.Now, Log: logger,
		Debounce: 2 * time.Second, Presence: 10 * time.Minute,
		Usage:        func() (*session.Usage, error) { return laptop.LoadUsage(usagePath()) },
		UsageChanged: usage,
		Commands: &agent.Commands{
			Act:         actions{aoe: c.Aoe, group: env("PEYK_AOE_GROUP", "personal"), claudeConfig: claudeConfig()},
			Keys:        keyring().Load,
			Origin:      o.String(),
			RPID:        o.Hostname(),
			Attachments: attachmentsDir(),
		},
	}
	if gh, err := github(ctx); err != nil {
		logger.Printf("not following CI: %v", err)
	} else {
		a.CI, a.CIPoll, a.CIWait = gh, 20*time.Second, 2*time.Minute
	}
	err = a.Run(ctx, pings, cmds)
	cancel()
	for i := 0; i < 2; i++ {
		if bg := <-bgErr; err == nil {
			err = bg
		}
	}
	logger.Print("offline")
	return err
}

// github reads workflow runs as the account gh is logged in to, so private
// repositories' runs are visible without a token of peyk's own.
func github(ctx context.Context) (laptop.GitHub, error) {
	out, err := laptop.Exec(ctx, "", "gh", "auth", "token", "--hostname", "github.com")
	if err != nil {
		return laptop.GitHub{}, err
	}
	return laptop.GitHub{
		Client:  &http.Client{Timeout: 15 * time.Second},
		BaseURL: "https://api.github.com",
		Token:   strings.TrimSpace(string(out)),
	}, nil
}

// billTo names the project that pays for a call made with your own login.
// golang.org/x/oauth2 ignores GOOGLE_CLOUD_QUOTA_PROJECT, and Identity
// Toolkit refuses user credentials that name no project.
type billTo struct {
	project string
	next    http.RoundTripper
}

func (b billTo) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Goog-User-Project", b.project)
	return b.next.RoundTrip(r)
}

func grantOwner(ctx context.Context, email string) error {
	p, err := project()
	if err != nil {
		return err
	}
	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return err
	}
	client.Transport = billTo{project: p, next: client.Transport}
	uid, err := owner.Grant(ctx, client, "https://identitytoolkit.googleapis.com", p, email)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) is the owner. Sign out and in again on the phone to pick it up.\n", email, uid)
	return nil
}

// importTasks adds tasks from a JSON array on stdin to the inbox.
func importTasks(ctx context.Context, in io.Reader, out io.Writer) error {
	c := collector()
	snaps, err := c.All(ctx)
	if err != nil {
		return err
	}
	repos, err := c.Repos()
	if err != nil {
		return err
	}
	fs, _, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()
	n, err := inbox.Import(ctx, in, session.Projects(snaps, repos), fs)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Imported %d tasks.\n", n)
	return nil
}
