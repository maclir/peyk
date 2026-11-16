// Package launchd describes the per-user launchd jobs that keep the agent and
// its menu bar app running.
package launchd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Agent and Menu are the jobs' labels. The menu bar app addresses the agent
// by its label, so the two must agree.
const (
	Agent = "com.peyk.agent"
	Menu  = "com.peyk.menu"
)

// Job is one launchd job. KeepAlive restarts it whenever it exits; without
// it, the job runs once per login and stays gone after it quits.
type Job struct {
	Label     string
	Program   []string
	Dir       string
	Env       map[string]string
	Log       string
	KeepAlive bool
}

// Plist is the job as a property list for ~/Library/LaunchAgents.
func (j Job) Plist() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	str := func(key, v string) {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", key, esc(v))
	}
	str("Label", j.Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range j.Program {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(a))
	}
	b.WriteString("\t</array>\n")
	if j.Dir != "" {
		str("WorkingDirectory", j.Dir)
	}
	if len(j.Env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		keys := make([]string, 0, len(j.Env))
		for k := range j.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", esc(k), esc(j.Env[k]))
		}
		b.WriteString("\t</dict>\n")
	}
	if j.Log != "" {
		str("StandardOutPath", j.Log)
		str("StandardErrorPath", j.Log)
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	if j.KeepAlive {
		// A crash loop, such as expired credentials, retries this often
		// rather than as fast as the process can fail.
		b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	}
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n</dict>\n</plist>\n")
	return b.Bytes()
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Captured lists the variables a job inherits from the shell that installs
// it. launchd starts jobs with almost no environment, and these choose which
// tools, Claude Code profile and Google login the agent uses. The project's
// own settings are not among them: make reads those from .env on every start.
var Captured = []string{
	"PATH",
	"CLAUDE_CONFIG_DIR",
	"GOOGLE_APPLICATION_CREDENTIALS",
	"CLOUDSDK_CONFIG",
	"GOPATH", "GOMODCACHE", "GOCACHE", "GOFLAGS", "GOPROXY",
	"PEYK_AOE_PROFILE", "PEYK_AOE_GROUP", "PEYK_PROJECTS_ROOT", "PEYK_AGENT_ACCOUNT",
}

// Capture picks the set variables in Captured out of lookup.
func Capture(lookup func(string) (string, bool)) map[string]string {
	env := map[string]string{}
	for _, k := range Captured {
		if v, ok := lookup(k); ok && v != "" {
			env[k] = v
		}
	}
	return env
}

// Path is where a job's plist lives.
func Path(home, label string) string {
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

// Domain is the logged-in user's GUI domain, where per-user jobs run.
func Domain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}
