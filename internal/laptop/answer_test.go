package laptop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// dialog fakes tmux and aoe around a session whose transcript has an open
// question. Esc writes the given result, as Claude Code would.
type dialog struct {
	t      *testing.T
	tr     session.Transcript
	screen string
	onEsc  string
	onSend string
	calls  []string
}

func newDialog(t *testing.T) *dialog {
	claude := t.TempDir()
	work := "/Users/me/code/knip"
	writeTranscript(t, claude, work, "conv", time.Now(), ask("2026-11-14T09:00:00Z", "toolu_1"))
	return &dialog{
		t:      t,
		tr:     session.Transcript{Dir: TranscriptDir(claude, work), ID: "conv"},
		screen: "←  ☐ Colour  ☐ Pets  ✔ Submit  →\nEnter to select · Tab/Arrow keys to navigate · Esc to cancel\n",
		onEsc:  `{"type":"user","message":{"content":[{"type":"tool_result","content":"rejected","is_error":true,"tool_use_id":"toolu_1"}]}}`,
		onSend: human("2026-11-14T09:01:00Z", `"Colour: Blue"`),
	}
}

func (d *dialog) run(_ context.Context, _, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	d.calls = append(d.calls, call)
	switch {
	case strings.HasPrefix(call, "tmux list-sessions"):
		return []byte("aoe_other_aaaaaaaa\naoe_knip_7ab0c363\n"), nil
	case strings.HasPrefix(call, "tmux capture-pane"):
		return []byte(d.screen), nil
	case strings.HasPrefix(call, "tmux send-keys"):
		return nil, d.append(d.onEsc)
	case strings.HasPrefix(call, "aoe -p personal send "):
		if d.onSend != "" {
			return nil, d.append(d.onSend)
		}
	}
	return nil, nil
}

func (d *dialog) append(line string) error {
	f, err := os.OpenFile(filepath.Join(d.tr.Dir, d.tr.ID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		d.t.Fatal(err)
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func (d *dialog) answer() error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	return Aoe{Profile: "personal", Run: d.run}.Answer(ctx, "7ab0c36356b64513", d.tr, "toolu_1", "Colour: Blue", time.Millisecond)
}

func TestAnswerDismissesTheDialogThenSendsTheAnswer(t *testing.T) {
	d := newDialog(t)
	if err := d.answer(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux list-sessions -F #{session_name}",
		"tmux capture-pane -p -t aoe_knip_7ab0c363",
		"tmux send-keys -t aoe_knip_7ab0c363 Escape",
		"aoe -p personal send -- 7ab0c36356b64513 Colour: Blue",
	}
	if strings.Join(d.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(d.calls, "\n"))
	}
}

func TestAnswerLeavesAScreenWithoutTheDialogAlone(t *testing.T) {
	d := newDialog(t)
	d.screen = "❯ \n"
	if err := d.answer(); err == nil {
		t.Fatal("answered without the dialog on screen")
	}
	for _, c := range d.calls {
		if strings.Contains(c, "send") {
			t.Fatalf("pressed or sent something: %s", c)
		}
	}
}

func TestAnswerStopsWhenTheTerminalAnsweredFirst(t *testing.T) {
	d := newDialog(t)
	d.onEsc = result("toolu_1")
	if err := d.answer(); !errors.Is(err, ErrAnswered) {
		t.Fatalf("err = %v", err)
	}
	if last := d.calls[len(d.calls)-1]; !strings.HasPrefix(last, "tmux send-keys") {
		t.Fatalf("sent after the terminal answered: %s", last)
	}

	d = newDialog(t)
	f, _ := os.OpenFile(filepath.Join(d.tr.Dir, d.tr.ID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(result("toolu_1") + "\n")
	f.Close()
	if err := d.answer(); !errors.Is(err, ErrAnswered) || len(d.calls) != 0 {
		t.Fatalf("err = %v, calls = %v", err, d.calls)
	}
}

func TestAnswerFailsWhenThePromptIsNotRecorded(t *testing.T) {
	d := newDialog(t)
	d.onSend = ""
	if err := d.answer(); err == nil || !strings.Contains(err.Error(), "did not record the prompt") {
		t.Fatalf("err = %v", err)
	}
}
