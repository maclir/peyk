// Package laptop reads session state from the machine the agents run on: aoe,
// git, and Claude Code transcripts.
package laptop

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a program in dir and returns its standard output.
type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Exec runs real programs.
func Exec(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
