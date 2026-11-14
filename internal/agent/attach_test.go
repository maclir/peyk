package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/store"
)

func putImage(t *testing.T, mem *store.Memory, data []byte, typ string) string {
	t.Helper()
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	if err := mem.Put(context.Background(), store.Attachments, id, store.Attachment{Data: data, Type: typ}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDispatchSavesImagesAndNamesThemInThePrompt(t *testing.T) {
	ph := newPhone(t)
	a, mem, acts := commandAgent(t, ph)
	dir := filepath.Join(t.TempDir(), "attachments")
	a.Commands.Attachments = dir
	png := putImage(t, mem, []byte("png bytes"), "image/png")
	jpg := putImage(t, mem, []byte("jpeg bytes"), "image/jpeg")

	in := ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "Fix this", Images: []string{png, jpg}}, a.Now())
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if res := mem.Updated(store.Commands, "c1"); res["status"] != command.Done {
		t.Fatalf("result = %v", res)
	}
	want := "send k: Fix this\n\nAttached images:\n- " + filepath.Join(dir, png+".png") + "\n- " + filepath.Join(dir, jpg+".jpg")
	if got := strings.Join(acts.calls, "|"); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, png+".png")); !bytes.Equal(got, []byte("png bytes")) {
		t.Fatalf("saved image = %q", got)
	}
}

func TestDispatchRefusesAnImageThatDoesNotMatchItsHash(t *testing.T) {
	ph := newPhone(t)
	a, mem, acts := commandAgent(t, ph)
	a.Commands.Attachments = t.TempDir()
	signed := sha256.Sum256([]byte("what the phone signed"))
	id := hex.EncodeToString(signed[:])
	_ = mem.Put(context.Background(), store.Attachments, id, store.Attachment{Data: []byte("swapped"), Type: "image/png"})

	in := ph.command("c1", command.Payload{Type: command.Dispatch, Project: "knip", Task: "t1", Text: "x", Images: []string{id}, Clear: true}, a.Now())
	if err := a.execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	res := mem.Updated(store.Commands, "c1")
	if res["status"] != command.Failed || !strings.Contains(res["error"].(string), "does not match its hash") {
		t.Fatalf("result = %v", res)
	}
	if len(acts.calls) != 0 {
		t.Fatalf("a refused image must leave the worker untouched: %v", acts.calls)
	}
}
