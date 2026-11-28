package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/laptop"
	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

func TestPublishStoresHTMLAndAttachesItToTheMatchingWorker(t *testing.T) {
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("/tmp", "peyk-pub-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "p.sock")
	protos := &laptop.Prototypes{Path: filepath.Join(dir, "prototypes.json")}

	c := &fakeCollector{}
	makeSnap := func() session.Snapshot {
		tr := session.Transcript{Tool: session.Antigravity, ID: "conv-ag"}
		return session.Snapshot{
			ID: "g", Title: "gemi", Path: filepath.Join(dir, "gemi"),
			Tool: session.Antigravity, State: session.Working,
			Transcript: tr,
			Prototypes: protos.Find(tr),
		}
	}
	c.set(makeSnap())

	mem := store.NewMemory()
	a := newAgent(c, mem)
	a.Commands = &Commands{Origin: testOrigin}
	pubReqs := make(chan PublishCall, 2)
	a.PublishRequests = pubReqs
	a.LatestPrototype = protos.LatestHostedID
	a.RecordPrototype = func(conv string, p session.Prototype) error {
		if err := protos.Record(conv, p); err != nil {
			return err
		}
		c.set(makeSnap())
		return nil
	}

	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ServePublish(ctx, ln, pubReqs)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, nil, nil) }()
	waitFor(t, func() bool { _, ok := mem.Get(store.Sessions, "g"); return ok })

	html := "<!doctype html><html><head><title>Working tag</title></head><body>Hi</body></html>"
	resp, err := Publish(ctx, sock, PublishRequest{
		Dir:  filepath.Join(dir, "gemi", "sub"),
		HTML: html,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(resp.ID) != 64 || resp.URL != testOrigin+"/p/"+resp.ID {
		t.Fatalf("resp = %+v", resp)
	}
	stored, ok := mem.Get(store.Prototypes, resp.ID)
	if !ok {
		t.Fatal("prototype document was not stored")
	}
	doc := stored.(store.Prototype)
	if doc.HTML != html || doc.Title != "Working tag" || doc.Kind != session.PrototypeHTML {
		t.Fatalf("stored = %+v", doc)
	}
	snapVal, _ := mem.Get(store.Sessions, "g")
	snap := snapVal.(session.Snapshot)
	if len(snap.Prototypes) != 1 || snap.Prototypes[0].URL != "/p/"+resp.ID || snap.Prototypes[0].Title != "Working tag" {
		t.Fatalf("session prototypes = %+v", snap.Prototypes)
	}

	// Re-publishing in the same conversation updates the same prototype id in place.
	updatedHTML := "<!doctype html><html><head><title>Working tag v2</title></head><body>Updated</body></html>"
	updatedResp, err := Publish(ctx, sock, PublishRequest{
		Dir:  filepath.Join(dir, "gemi"),
		HTML: updatedHTML,
	})
	if err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if updatedResp.ID != resp.ID || updatedResp.URL != resp.URL {
		t.Fatalf("re-publish created a new id %q, want %q", updatedResp.ID, resp.ID)
	}
	stored, _ = mem.Get(store.Prototypes, resp.ID)
	if doc := stored.(store.Prototype); doc.HTML != updatedHTML || doc.Title != "Working tag v2" {
		t.Fatalf("updated stored = %+v", doc)
	}
	snapVal, _ = mem.Get(store.Sessions, "g")
	if snap := snapVal.(session.Snapshot); len(snap.Prototypes) != 1 || snap.Prototypes[0].Title != "Working tag v2" {
		t.Fatalf("after update session prototypes = %+v", snap.Prototypes)
	}

	// Passing New creates a separate prototype id alongside the first.
	secondResp, err := Publish(ctx, sock, PublishRequest{
		Dir:   filepath.Join(dir, "gemi"),
		Title: "Second prototype",
		HTML:  "<p>Another</p>",
		New:   true,
	})
	if err != nil {
		t.Fatalf("Publish --new: %v", err)
	}
	if secondResp.ID == resp.ID || len(secondResp.ID) != 64 {
		t.Fatalf("secondResp = %+v, want distinct 64-hex id", secondResp)
	}
	snapVal, _ = mem.Get(store.Sessions, "g")
	if snap := snapVal.(session.Snapshot); len(snap.Prototypes) != 2 {
		t.Fatalf("after --new session prototypes = %+v", snap.Prototypes)
	}

	// Publishing from an unrelated directory still uploads and returns the URL.
	otherHTML := "<p>Standalone</p>"
	otherResp, err := Publish(ctx, sock, PublishRequest{Dir: filepath.Join(dir, "elsewhere"), Title: "Standalone", HTML: otherHTML})
	if err != nil {
		t.Fatalf("Publish outside session: %v", err)
	}
	if _, ok := mem.Get(store.Prototypes, otherResp.ID); !ok {
		t.Fatal("standalone prototype was not stored")
	}

	// Validation errors are returned cleanly without stopping the agent.
	if _, err := Publish(ctx, sock, PublishRequest{HTML: "   "}); err == nil {
		t.Fatal("empty HTML should fail")
	}
	if _, err := Publish(ctx, sock, PublishRequest{HTML: strings.Repeat("a", store.MaxPrototypeBytes+1)}); err == nil {
		t.Fatal("oversized HTML should fail")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not exit")
	}
}

func TestExtractHTMLTitleAndValidation(t *testing.T) {
	if got := ExtractHTMLTitle("<HTML><HEAD><TITLE>  A &amp; B \n</TITLE></HEAD></HTML>", "fallback"); got != "A & B" {
		t.Fatalf("title = %q", got)
	}
	if got := ExtractHTMLTitle("<p>no title</p>", "working-tag"); got != "working-tag" {
		t.Fatalf("fallback = %q", got)
	}
	if got := ExtractHTMLTitle("<p>no title</p>", ""); got != "Prototype" {
		t.Fatalf("default = %q", got)
	}
	if err := ValidatePrototypeHTML("\xff\xfe"); err == nil {
		t.Fatal("invalid UTF-8 should fail")
	}
}
