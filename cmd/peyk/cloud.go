package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/inbox"
	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/owner"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

func socketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peyk", "agent.sock")
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
	watchErr := make(chan error, 1)
	go func() {
		err := watcher.Forward(ctx, pings)
		watchErr <- err
		if err != nil {
			cancel()
		}
	}()

	logger := log.New(os.Stderr, "peyk: ", log.LstdFlags)
	logger.Printf("publishing to %s; Ctrl-C to go offline", p)
	a := &agent.Agent{
		Collect: collector(), Store: fs, Watch: watcher, Now: time.Now, Log: logger,
		Debounce: 2 * time.Second, Presence: 10 * time.Minute,
	}
	err = a.Run(ctx, pings)
	cancel()
	if werr := <-watchErr; err == nil {
		err = werr
	}
	logger.Print("offline")
	return err
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
