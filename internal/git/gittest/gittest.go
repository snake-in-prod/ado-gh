// Package gittest provides a stubbed git runner for git.Client.Run in tests.
package gittest

import (
	"context"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/git"
)

// Runner is a stubbed git runner: Replies maps "arg arg ..." to stdout, Fails maps it to an
// error, and Calls records every invocation in that joined form. The zero value is usable.
type Runner struct {
	Replies map[string]string
	Fails   map[string]error
	Calls   []string
}

// Run satisfies git.Client.Run. Unanswered rev-parse, symbolic-ref and config --get calls fail
// with exit 1, like git for a missing ref, branch or config key; other calls succeed silently.
func (r *Runner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.Calls = append(r.Calls, key)
	if err, ok := r.Fails[key]; ok {
		return nil, err
	}
	if out, ok := r.Replies[key]; ok {
		return []byte(out), nil
	}
	if len(args) > 0 && (args[0] == "rev-parse" || args[0] == "symbolic-ref") ||
		len(args) > 1 && args[0] == "config" && args[1] == "--get" {
		return nil, &git.Error{Args: args, ExitCode: 1}
	}
	return nil, nil
}

// Mutations returns the recorded calls that change the repository or a remote.
func (r *Runner) Mutations() []string {
	var out []string
	for _, c := range r.Calls {
		cmd, rest, _ := strings.Cut(c, " ")
		flag, _, _ := strings.Cut(rest, " ")
		switch {
		case cmd == "fetch", cmd == "checkout", cmd == "merge", cmd == "reset", cmd == "push",
			cmd == "config" && !strings.HasPrefix(flag, "--get"),
			cmd == "branch" && (flag == "-d" || flag == "-D"):
			out = append(out, c)
		}
	}
	return out
}
