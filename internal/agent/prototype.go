package agent

import (
	"fmt"
	"time"

	"github.com/maclir/peyk/internal/session"
)

// The instruction is fixed on the laptop, so the signed kind alone decides
// what is asked. It overrides the worker's standing rule to test and commit
// every change, since a prototype leaves the repository as it was.
var prototypeAsks = map[string]string{
	session.PrototypeHTML:   "build it as a plain HTML page",
	session.PrototypeDesign: "build it on a canvas made from the Design Artifact type",
	session.PrototypeAny:    "build it as a plain HTML page or on a canvas made from the Design Artifact type, whichever suits the task",
}

func prototypeText(text, kind string) (string, error) {
	if kind == "" {
		return text, nil
	}
	ask, ok := prototypeAsks[kind]
	if !ok {
		return "", fmt.Errorf("no prototype kind %q", kind)
	}
	return fmt.Sprintf("%s\n\nDeliver this as a prototype, not a code change: %s and publish it with the Artifact tool. Read the repository as needed, but do not edit, test or commit it.", text, ask), nil
}

// publishedSince returns the newest page published at or after since.
// Prototypes are newest first.
func publishedSince(ps []session.Prototype, since time.Time) string {
	if len(ps) == 0 || ps[0].At.Before(since) {
		return ""
	}
	return ps[0].URL
}
