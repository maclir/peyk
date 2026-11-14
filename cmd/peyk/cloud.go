package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"

	"github.com/maclir/peyk/internal/agent"
	"github.com/maclir/peyk/internal/owner"
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

// runAgent publishes as the peyk-agent service account, impersonated from
// your own login. The token it holds can reach this project's Firestore and
// nothing else, and no key file exists to leak.
func runAgent(ctx context.Context) error {
	p, err := project()
	if err != nil {
		return err
	}
	ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
		TargetPrincipal: env("PEYK_AGENT_ACCOUNT", "peyk-agent@"+p+".iam.gserviceaccount.com"),
		Scopes:          []string{"https://www.googleapis.com/auth/datastore"},
	})
	if err != nil {
		return fmt.Errorf("impersonate agent account: %w", err)
	}
	fs, err := store.NewFirestore(ctx, p, option.WithTokenSource(ts))
	if err != nil {
		return err
	}
	defer fs.Close()

	ln, err := agent.Listen(socketPath())
	if err != nil {
		return err
	}
	pings := make(chan struct{}, 1)
	go agent.Serve(ctx, ln, pings)

	logger := log.New(os.Stderr, "peyk: ", log.LstdFlags)
	logger.Printf("publishing to %s; Ctrl-C to go offline", p)
	a := &agent.Agent{
		Collect: collector(), Store: fs, Now: time.Now, Log: logger,
		Debounce: 2 * time.Second, Heartbeat: 10 * time.Minute,
	}
	err = a.Run(ctx, pings)
	logger.Print("offline")
	return err
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
	uid, err := owner.Grant(ctx, client, "https://identitytoolkit.googleapis.com", p, email)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) is the owner. Sign out and in again on the phone to pick it up.\n", email, uid)
	return nil
}
