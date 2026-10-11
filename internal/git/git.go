// Package git shells out to the git binary for repository context.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ErrNotGitRepo matches (via errors.Is) a git failure outside a repository.
var ErrNotGitRepo = errors.New("not a git repository")

// ErrNotOnAnyBranch is returned by CurrentBranch on a detached HEAD.
var ErrNotOnAnyBranch = errors.New("git: not on any branch")

// Error is a git command that exited non-zero.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	if s := strings.TrimSpace(e.Stderr); s != "" {
		return s
	}
	return fmt.Sprintf("git %s: exit status %d", strings.Join(e.Args, " "), e.ExitCode)
}

// Is reports ErrNotGitRepo for git's "not a git repository" failure.
// ponytail: matches git's English message; a localized git (LANG=de...) won't match.
func (e *Error) Is(target error) bool {
	return target == ErrNotGitRepo && strings.Contains(e.Stderr, "not a git repository")
}

// Remote is one entry of `git remote -v`.
type Remote struct {
	Name     string
	FetchURL string
	PushURL  string
	// Resolved is the remote.<name>.ado-resolved value ("base" = default repo); only
	// ResolvedRemotes fills it.
	Resolved string
}

// Client runs git in Dir ("" = current directory).
type Client struct {
	Dir string
	// Run executes git and returns stdout; nil runs the git binary. Tests replace it.
	Run func(ctx context.Context, dir string, args ...string) ([]byte, error)
	// Stream executes git with s attached; nil runs the git binary. Tests replace it.
	Stream func(ctx context.Context, dir string, s Streams, args ...string) error
}

// Streams are the standard streams a streamed git command is attached to.
type Streams struct {
	In          io.Reader
	Out, ErrOut io.Writer
}

// ExecStream runs git with args attached to s, so the user sees git's progress and prompts.
// Failures are *Error with an empty Stderr: git already wrote it to s.ErrOut.
func (c *Client) ExecStream(ctx context.Context, s Streams, args ...string) error {
	if c.Stream != nil {
		return c.Stream(ctx, c.Dir, s, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.In, s.Out, s.ErrOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &Error{Args: args, ExitCode: exitErr.ExitCode()}
	}
	return err
}

// Exec runs git with args and returns stdout; failures are *Error.
func (c *Client) Exec(ctx context.Context, args ...string) ([]byte, error) {
	if c.Run != nil {
		return c.Run(ctx, c.Dir, args...)
	}
	return run(ctx, c.Dir, args...)
}

func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// exec.LookPath refuses a git binary from the current directory (ErrDot).
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.Bytes(), &Error{Args: args, ExitCode: exitErr.ExitCode(), Stderr: stderr.String()}
	}
	return stdout.Bytes(), err
}

// Remotes lists the configured remotes in git's order.
func (c *Client) Remotes(ctx context.Context) ([]Remote, error) {
	out, err := c.Exec(ctx, "remote", "-v")
	if err != nil {
		return nil, err
	}
	return parseRemotes(string(out)), nil
}

// ResolvedRemotes is Remotes with each remote's ado-resolved config value filled in.
func (c *Client) ResolvedRemotes(ctx context.Context) ([]Remote, error) {
	remotes, err := c.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	out, err := c.Exec(ctx, "config", "--get-regexp", `^remote\..*\.ado-resolved$`)
	var gitErr *Error
	if errors.As(err, &gitErr) && gitErr.ExitCode == 1 { // no key set
		return remotes, nil
	}
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(string(out)) {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		// Trim both ends rather than split on ".", so dotted remote names survive.
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".ado-resolved")
		for i := range remotes {
			if remotes[i].Name == name {
				remotes[i].Resolved = value
			}
		}
	}
	return remotes, nil
}

// parseRemotes parses "name\turl (fetch|push)[ filter]" lines; URLs may contain spaces.
func parseRemotes(out string) []Remote {
	var remotes []Remote
	index := map[string]int{}
	for line := range strings.Lines(out) {
		name, rest, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		if !ok {
			continue
		}
		i, ok := index[name]
		if !ok {
			i = len(remotes)
			index[name] = i
			remotes = append(remotes, Remote{Name: name})
		}
		r := &remotes[i]
		if u, _, ok := strings.Cut(rest, " (fetch)"); ok && r.FetchURL == "" {
			r.FetchURL = u
		} else if u, _, ok := strings.Cut(rest, " (push)"); ok && r.PushURL == "" {
			r.PushURL = u
		}
	}
	return remotes
}

// CurrentBranch returns the checked-out branch name without refs/heads/.
func (c *Client) CurrentBranch(ctx context.Context) (string, error) {
	out, err := c.Exec(ctx, "symbolic-ref", "--quiet", "HEAD")
	var gitErr *Error
	if errors.As(err, &gitErr) && gitErr.ExitCode == 1 && gitErr.Stderr == "" {
		return "", ErrNotOnAnyBranch
	}
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "refs/heads/"), nil
}
