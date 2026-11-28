package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maclir/peyk/internal/agent"
)

func publishPrototype(ctx context.Context, args []string, sockPath, cwd string, out io.Writer) error {
	path, title, isNew, err := parsePublishArgs(args)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(raw)
	if err := agent.ValidatePrototypeHTML(body); err != nil {
		return err
	}
	if title == "" {
		base := filepath.Base(path)
		stem := strings.TrimSuffix(strings.TrimSuffix(base, ".html"), ".htm")
		title = agent.ExtractHTMLTitle(body, stem)
	}
	resp, err := agent.Publish(ctx, sockPath, agent.PublishRequest{
		Dir:   cwd,
		Title: title,
		HTML:  body,
		New:   isNew,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, resp.URL)
	return nil
}

func parsePublishArgs(args []string) (path, title string, isNew bool, err error) {
	const msg = "usage: peyk publish [--new] [--title <title>] <file>"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--new" || a == "-new":
			isNew = true
		case a == "--title" || a == "-title" || a == "-t":
			if i+1 >= len(args) {
				return "", "", false, fmt.Errorf("%s", msg)
			}
			i++
			title = args[i]
		case strings.HasPrefix(a, "--title="):
			title = strings.TrimPrefix(a, "--title=")
		case strings.HasPrefix(a, "-title="):
			title = strings.TrimPrefix(a, "-title=")
		case strings.HasPrefix(a, "-t="):
			title = strings.TrimPrefix(a, "-t=")
		case strings.HasPrefix(a, "-"):
			return "", "", false, fmt.Errorf("unknown flag %q\n%s", a, msg)
		default:
			if path != "" {
				return "", "", false, fmt.Errorf("%s", msg)
			}
			path = a
		}
	}
	if path == "" {
		return "", "", false, fmt.Errorf("%s", msg)
	}
	return path, strings.TrimSpace(title), isNew, nil
}
