package notify

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto/ecdh"
	"crypto/rand"

	"github.com/maclir/peyk/internal/session"
)

func TestChanges(t *testing.T) {
	at := time.Now()
	prev := map[string]session.Snapshot{
		"k": {ID: "k", Title: "knip", State: session.Working},
		"h": {ID: "h", Title: "hamta", State: session.Working},
		"z": {ID: "z", Title: "zynk", State: session.Idle},
		"a": {ID: "a", Title: "areh", State: session.Idle},
		"p": {ID: "p", Title: "popq", State: session.Working},
		"q": {ID: "q", Title: "quiz", State: session.Working},
	}
	next := map[string]session.Snapshot{
		"k": {ID: "k", Title: "knip", State: session.Unpushed, Git: &session.Git{Unpushed: 2, Last: &session.Commit{Hash: "3f2a1c4", At: at}},
			Message: &session.Message{Text: "Committed `3f2a1c4`.\n\n**All** tests pass."}},
		"h": {ID: "h", Title: "hamta", State: session.NeedsInput, Message: &session.Message{Text: "Include archived rows?"}},
		"z": {ID: "z", Title: "zynk", State: session.Working},
		"a": {ID: "a", Title: "areh", State: session.Idle},
		"p": {ID: "p", Title: "popq", State: session.Working},
		"n": {ID: "n", Title: "new", State: session.NeedsInput},
		"q": {ID: "q", Title: "quiz", State: session.NeedsInput, Message: &session.Message{Text: "Reading the code."},
			Question: &session.Question{Items: []session.QuestionItem{{Question: "Which `colour`?"}, {Question: "Which pets?"}}}},
	}
	got := Changes(prev, next)
	if len(got) != 3 {
		t.Fatalf("notes = %+v", got)
	}
	if got[0].Session != "h" || got[0].Title != "hamta needs you" || got[0].Body != "Include archived rows?" || got[0].URL != "/#s-h" {
		t.Errorf("hamta note = %+v", got[0])
	}
	if got[1].Title != "knip finished" || got[1].Body != "3f2a1c4 · 2 unpushed\nCommitted 3f2a1c4. All tests pass." {
		t.Errorf("knip note = %+v", got[1])
	}
	if got[2].Title != "quiz needs you" || got[2].Body != "Which colour?\nWhich pets?" {
		t.Errorf("quiz note = %+v", got[2])
	}
}

func TestChangesNotesAFailedRunOnce(t *testing.T) {
	git := func(state session.CIState) *session.Git {
		return &session.Git{Last: &session.Commit{Hash: "3f2a1c4", Subject: "Ship it"},
			CI: &session.CI{SHA: "3f2a1c4e", Workflow: "Test and deploy", State: state}}
	}
	running := map[string]session.Snapshot{"k": {ID: "k", Title: "knip", State: session.Idle, Git: git(session.CIRunning)}}
	failed := map[string]session.Snapshot{"k": {ID: "k", Title: "knip", State: session.Idle, Git: git(session.CIFailure)}}
	got := Changes(running, failed)
	if len(got) != 1 || got[0].Title != "knip: Test and deploy failed" || got[0].Body != "3f2a1c4 Ship it" || got[0].URL != "/#s-k" {
		t.Fatalf("notes = %+v", got)
	}
	if got := Changes(failed, failed); len(got) != 0 {
		t.Fatalf("a failure already seen was noted again: %+v", got)
	}
	passed := map[string]session.Snapshot{"k": {ID: "k", Title: "knip", State: session.Idle, Git: git(session.CISuccess)}}
	if got := Changes(running, passed); len(got) != 0 {
		t.Fatalf("a passing run was noted: %+v", got)
	}
}

func TestLoadOrCreateKeysPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	a, err := LoadOrCreateKeys(path)
	if err != nil || a.Public == "" || a.Private == "" {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := LoadOrCreateKeys(path)
	if err != nil || b != a {
		t.Fatalf("second load = %+v, %v", b, err)
	}
}

// phoneSubscription makes the keys a browser would hand over when subscribing.
func phoneSubscription(t *testing.T, endpoint string) Subscription {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	enc := base64.RawURLEncoding
	return Subscription{Endpoint: endpoint, P256dh: enc.EncodeToString(priv.PublicKey().Bytes()), Auth: enc.EncodeToString(auth)}
}

func TestSendSignsAndReportsGoneSubscriptions(t *testing.T) {
	var gotAuth, gotTopic string
	var gotLen int
	status := http.StatusCreated
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotTopic = r.Header.Get("Authorization"), r.Header.Get("Topic")
		b, _ := io.ReadAll(r.Body)
		gotLen = len(b)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	keys, err := LoadOrCreateKeys(filepath.Join(t.TempDir(), "v.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := Sender{Keys: keys, Subscriber: "https://peyk.test", Client: srv.Client()}
	sub := phoneSubscription(t, srv.URL+"/push/abc")
	note := Note{Session: "2af133e3", Title: "knip finished", Body: "secret commit message"}

	if err := s.Send(context.Background(), sub, note); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") || !strings.Contains(gotAuth, "k="+keys.Public) {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotTopic != "2af133e3" || gotLen == 0 {
		t.Errorf("topic %q, body %d bytes", gotTopic, gotLen)
	}

	status = http.StatusGone
	if err := s.Send(context.Background(), sub, note); err != ErrGone {
		t.Fatalf("410 = %v, want ErrGone", err)
	}
	status = http.StatusTooManyRequests
	if err := s.Send(context.Background(), sub, note); err == nil || err == ErrGone {
		t.Fatalf("429 = %v, want an error", err)
	}
}
