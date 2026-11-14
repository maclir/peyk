// Package notify decides which session changes deserve a phone notification
// and delivers them with Web Push. Payloads are encrypted to each phone's own
// key, so the push service relays them without reading them.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/maclir/peyk/internal/session"
)

// Note is one notification.
type Note struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	URL     string `json:"url"`
}

// Changes returns a note for each session that started needing you,
// finished a turn, or whose pushed commit's workflow failed between two
// refreshes. A session absent from prev is new to the agent, not changed, so
// it gets none.
func Changes(prev, next map[string]session.Snapshot) []Note {
	var notes []Note
	for id, n := range next {
		p, ok := prev[id]
		if !ok {
			continue
		}
		if ci := ciOf(n); ci != nil && ci.State == session.CIFailure && !sameFailure(ciOf(p), ci) {
			notes = append(notes, Note{Session: id, Title: n.Title + ": " + ci.Workflow + " failed", Body: ciBody(n), URL: "/#s-" + id})
		}
		if p.State == n.State {
			continue
		}
		var title string
		switch {
		case n.State == session.NeedsInput:
			title = n.Title + " needs you"
		case n.State == session.Error:
			title = n.Title + " hit an error"
		case p.State == session.Working && n.State != session.Working:
			title = n.Title + " finished"
		default:
			continue
		}
		notes = append(notes, Note{Session: id, Title: title, Body: body(n), URL: "/#s-" + id})
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].Session < notes[j].Session })
	return notes
}

func ciOf(s session.Snapshot) *session.CI {
	if s.Git == nil {
		return nil
	}
	return s.Git.CI
}

func sameFailure(a, b *session.CI) bool {
	return a != nil && a.State == session.CIFailure && a.SHA == b.SHA
}

func ciBody(s session.Snapshot) string {
	if g := s.Git; g.Last != nil && g.Unpushed == 0 {
		return g.Last.Hash + " " + oneLine(g.Last.Subject)
	}
	return s.Git.CI.SHA[:min(7, len(s.Git.CI.SHA))]
}

func body(s session.Snapshot) string {
	var parts []string
	if g := s.Git; g != nil {
		switch {
		case g.Dirty > 0:
			parts = append(parts, fmt.Sprintf("%d uncommitted files", g.Dirty))
		case g.Unpushed > 0 && g.Last != nil:
			parts = append(parts, fmt.Sprintf("%s · %d unpushed", g.Last.Hash, g.Unpushed))
		}
	}
	if s.Question != nil {
		for _, q := range s.Question.Items {
			parts = append(parts, oneLine(q.Question))
		}
	} else if s.Message != nil {
		parts = append(parts, oneLine(s.Message.Text))
	}
	return strings.Join(parts, "\n")
}

func oneLine(text string) string {
	line := strings.Join(strings.Fields(strings.NewReplacer("`", "", "*", "", "#", "").Replace(text)), " ")
	if r := []rune(line); len(r) > 160 {
		line = string(r[:159]) + "…"
	}
	return line
}

// Subscription is one phone's push endpoint.
type Subscription struct {
	ID       string `firestore:"-"`
	Endpoint string `firestore:"endpoint"`
	P256dh   string `firestore:"p256dh"`
	Auth     string `firestore:"auth"`
}

// Keys is the laptop's VAPID key pair. The phone subscribes with the public
// half, and the push service accepts only messages signed with the private one.
type Keys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// LoadOrCreateKeys reads the key pair, creating it on first use.
func LoadOrCreateKeys(path string) (Keys, error) {
	var k Keys
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &k); err != nil || k.Public == "" || k.Private == "" {
			return k, fmt.Errorf("%s is not a VAPID key pair", path)
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	k.Private, k.Public, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return k, err
	}
	data, _ = json.Marshal(k)
	return k, os.WriteFile(path, data, 0o600)
}

// Sender delivers notes to subscriptions.
type Sender struct {
	Keys Keys
	// Subscriber identifies the sender to push services; an https URL.
	Subscriber string
	Client     webpush.HTTPClient
}

// ErrGone marks a subscription the push service no longer knows: the phone
// unsubscribed or the browser data was cleared.
var ErrGone = errors.New("subscription gone")

func (s Sender) Send(ctx context.Context, sub Subscription, n Note) error {
	payload, _ := json.Marshal(n)
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient: s.Client, Subscriber: s.Subscriber, TTL: 3600, Topic: topic(n.Session),
		Urgency: webpush.UrgencyHigh, VAPIDPublicKey: s.Keys.Public, VAPIDPrivateKey: s.Keys.Private,
	})
	if err != nil {
		return fmt.Errorf("push to %s: %w", host(sub.Endpoint), err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("push to %s: %s: %s", host(sub.Endpoint), resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// topic collapses a session's undelivered notes into its newest one.
func topic(session string) string {
	t := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, session)
	if len(t) > 32 {
		t = t[:32]
	}
	return t
}

func host(endpoint string) string {
	rest := strings.TrimPrefix(endpoint, "https://")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}
