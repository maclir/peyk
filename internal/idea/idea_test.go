package idea

import (
	"reflect"
	"strings"
	"testing"
)

func TestPromptAsksForTheLensesAndListsKnownIdeas(t *testing.T) {
	got, err := Prompt("Look across the whole repository.", Ask{Lenses: []string{"reuse", "experience"}, Max: 5}, []Idea{
		{Title: "Two date formatters", Status: Open},
		{Title: "Three confirm patterns", Status: Dismissed, Note: "confirm() is fine for unpair"},
		{Title: "Pair link expiry", Status: Dismissed},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Look across the whole repository.\n\nThis is an ideation task, not a code change.",
		"find at most 5 improvements",
		"- Reuse (reuse): ",
		"- One experience (experience): ",
		"Do not edit, test or commit anything.",
		"- Two date formatters\n",
		"- Three confirm patterns (dismissed: confirm() is fine for unpair)\n",
		"- Pair link expiry (dismissed)\n",
		"```peyk-ideas\n[{\"lens\": \"reuse\"",
		"lens is one of: reuse, experience.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Security") {
		t.Errorf("prompt names a lens it was not asked for:\n%s", got)
	}
}

func TestPromptRefusesWhatThePhoneCannotAsk(t *testing.T) {
	for name, ask := range map[string]Ask{
		"no lens":       {Max: 5},
		"unknown lens":  {Lenses: []string{"vibes"}, Max: 5},
		"unknown limit": {Lenses: []string{"reuse"}, Max: 7},
	} {
		if _, err := Prompt("x", ask, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPromptListsOnlyTheNewestKnownIdeas(t *testing.T) {
	var known []Idea
	for i := range maxKnown + 5 {
		known = append(known, Idea{Title: "idea " + strings.Repeat("x", i), Status: Open})
	}
	got, _ := Prompt("x", Ask{Lenses: []string{"reuse"}, Max: 3}, known)
	if strings.Contains(got, "- idea \n") || !strings.Contains(got, "- "+known[len(known)-1].Title+"\n") {
		t.Fatal("prompt did not keep the newest known ideas")
	}
}

func TestSplitTakesTheIdeasBlockOutOfTheReply(t *testing.T) {
	reply := "Found two.\n\n```peyk-ideas\n[{\"lens\":\"reuse\",\"title\":\"Two formatters\",\"why\":\"w\",\"refs\":[\"a.js:1\"],\"task\":\"Merge them\"}]\n```"
	rest, drafts := Split(reply)
	if rest != "Found two." {
		t.Errorf("rest = %q", rest)
	}
	want := []Draft{{Lens: "reuse", Title: "Two formatters", Why: "w", Refs: []string{"a.js:1"}, Task: "Merge them"}}
	if !reflect.DeepEqual(drafts, want) {
		t.Errorf("drafts = %+v", drafts)
	}
}

func TestSplitKeepsTextAfterTheBlock(t *testing.T) {
	rest, drafts := Split("Before\n```peyk-ideas\n[]\n```\nAfter")
	if rest != "Before\n\nAfter" || drafts == nil || len(drafts) != 0 {
		t.Fatalf("rest = %q, drafts = %v", rest, drafts)
	}
}

func TestSplitLeavesAReplyWithoutAReadableBlockWhole(t *testing.T) {
	for _, reply := range []string{
		"No ideas here.",
		"```peyk-ideas\n[{broken\n```",
		"```peyk-ideas\n[]",
	} {
		rest, drafts := Split(reply)
		if rest != reply || drafts != nil {
			t.Errorf("Split(%q) = %q, %v", reply, rest, drafts)
		}
	}
}

func TestAcceptKeepsWhatWasAskedForWithinTheLimit(t *testing.T) {
	ask := Ask{Lenses: []string{"reuse", "security"}, Max: 3}
	drafts := []Draft{
		{Lens: "reuse", Title: "  Two\nformatters ", Task: "Merge them", Refs: []string{"a.js:1", " "}},
		{Lens: "bugs", Title: "Not asked for", Task: "x"},
		{Lens: "reuse", Title: "No task"},
		{Lens: "security", Title: "already KNOWN", Task: "x"},
		{Lens: "security", Title: "two formatters", Task: "a copy of the first"},
		{Lens: "security", Title: "Pair link", Task: "Expire it"},
		{Lens: "reuse", Title: "Third", Task: "x"},
		{Lens: "reuse", Title: "Over the limit", Task: "x"},
	}
	got := Accept(drafts, ask, []Idea{{Title: "Already known"}})
	var titles []string
	for _, i := range got {
		titles = append(titles, i.Title)
		if i.Status != Open {
			t.Errorf("%q is %q", i.Title, i.Status)
		}
	}
	if want := []string{"Two formatters", "Pair link", "Third"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
	if !reflect.DeepEqual(got[0].Refs, []string{"a.js:1"}) || got[0].Prompt != "Merge them" {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestAcceptClipsLongFields(t *testing.T) {
	long := strings.Repeat("é", maxTitle+10)
	got := Accept([]Draft{{Lens: "reuse", Title: long, Task: "x"}}, Ask{Lenses: []string{"reuse"}, Max: 3}, nil)
	if n := len([]rune(got[0].Title)); n != maxTitle {
		t.Fatalf("title has %d runes", n)
	}
}
