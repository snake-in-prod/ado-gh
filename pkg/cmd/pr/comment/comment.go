// Package comment implements `ado pr comment`.
package comment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// statuses are the thread statuses --status accepts.
var statuses = []git.CommentThreadStatus{
	git.CommentThreadStatusValues.Active, git.CommentThreadStatusValues.Fixed, git.CommentThreadStatusValues.WontFix,
	git.CommentThreadStatusValues.Closed, git.CommentThreadStatusValues.ByDesign, git.CommentThreadStatusValues.Pending,
}

var errNoUserComments = errors.New("no comments found for current user")

type options struct {
	ref    string
	body   shared.BodyFlags
	thread int
	status git.CommentThreadStatus
	web    bool

	// file anchors a new thread to lines start..end of the file, on the left (base) side when set.
	file       string
	start, end int
	left       bool

	editLast, deleteLast, createIfNone, yes bool
}

// NewCmdComment returns the `pr comment` command.
func NewCmdComment(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	var status, lines, side string
	cmd := &cobra.Command{
		Use:   "comment [<number> | <url> | <branch>]",
		Short: "Add a comment to a pull request",
		Long: `Add a comment thread to a pull request, reply to a thread, or set a thread's status.

Without an argument, the pull request of the current branch is used. Without a body flag, the
text editor opens on a terminal. --thread replies to an existing thread; --status sets its
status (active, fixed, wontFix, closed, byDesign or pending), with or without a reply.

--file and --line anchor the new thread to a line or a range <n>:<m> of a changed file in the
latest iteration, on the new (right) side of the diff, or the old (left) side with --side left.
The path is relative to the repository root.

--edit-last and --delete-last act on your most recent comment in any thread of the pull
request, replies and file comments included. Without a body flag, --edit-last opens the text
editor on the current text. --delete-last asks for confirmation unless --yes is given.

The URL of the comment thread is printed.`,
		Example: `  ado pr comment 123 --body "Looks good"
  ado pr comment 123 --thread 7 --body "Done"
  ado pr comment 123 --thread 7 --status fixed
  ado pr comment 123 --file src/a.go --line 40:42 --body "nit"
  ado pr comment 123 --edit-last --body "Fixed typo"
  ado pr comment 123 --delete-last --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			fl := cmd.Flags()
			if fl.Changed("thread") && o.thread <= 0 {
				return cmdutil.FlagErrorf("invalid thread ID %d", o.thread)
			}
			if fl.Changed("status") {
				if !fl.Changed("thread") {
					return cmdutil.FlagErrorf("--status requires --thread")
				}
				var err error
				if o.status, err = parseStatus(status); err != nil {
					return err
				}
			}
			if err := parseInline(cmd, o, lines, side); err != nil {
				return err
			}
			if o.createIfNone && !o.editLast {
				return cmdutil.FlagErrorf("--create-if-none can only be used with --edit-last")
			}
			if o.yes && !o.deleteLast {
				return cmdutil.FlagErrorf("--yes should only be used with --delete-last")
			}
			if o.deleteLast && (o.body.Given() || o.body.Editor) {
				return cmdutil.FlagErrorf("should not provide comment body when using --delete-last")
			}
			if o.deleteLast && !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("should provide --yes to confirm deletion in non-interactive mode")
			}
			return run(cmd.Context(), f, o)
		},
	}
	shared.AddBodyFlags(cmd, &o.body)
	fl := cmd.Flags()
	fl.IntVar(&o.thread, "thread", 0, "Reply to the comment thread with this ID")
	fl.StringVar(&status, "status", "", "Set the status of the --thread: {active|fixed|wontFix|closed|byDesign|pending}")
	fl.StringVar(&o.file, "file", "", "Anchor the new thread to this file (with --line)")
	fl.StringVar(&lines, "line", "", "Line `<n>[:<m>]` of --file to comment on")
	fl.StringVar(&side, "side", "right", "Side of the diff for --line: {right|left}")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the pull request in the web browser")
	fl.BoolVar(&o.editLast, "edit-last", false, "Edit your most recent comment")
	fl.BoolVar(&o.deleteLast, "delete-last", false, "Delete your most recent comment")
	fl.BoolVar(&o.createIfNone, "create-if-none", false, "Create a new comment if you have none (with --edit-last)")
	fl.BoolVarP(&o.yes, "yes", "y", false, "Skip the delete confirmation prompt (with --delete-last)")
	cmd.MarkFlagsMutuallyExclusive("web", "body", "body-file", "editor")
	cmd.MarkFlagsMutuallyExclusive("web", "thread", "edit-last", "delete-last", "file")
	return cmd
}

// parseInline checks the --file, --line and --side flags and sets o.start, o.end and o.left.
func parseInline(cmd *cobra.Command, o *options, lines, side string) error {
	fl := cmd.Flags()
	file := fl.Changed("file")
	switch {
	case fl.Changed("line") && !file:
		return cmdutil.FlagErrorf("--line requires --file")
	case fl.Changed("side") && !file:
		return cmdutil.FlagErrorf("--side requires --file")
	case !file:
		return nil
	case !fl.Changed("line"):
		return cmdutil.FlagErrorf("--file requires --line")
	case strings.TrimSpace(o.file) == "":
		return cmdutil.FlagErrorf("--file cannot be empty")
	}
	switch strings.ToLower(side) {
	case "right":
	case "left":
		o.left = true
	default:
		return cmdutil.FlagErrorf("invalid value for --side: %q (valid values: right, left)", side)
	}
	var err error
	o.start, o.end, err = parseLines(lines)
	return err
}

// parseStatus maps s case-insensitively to a thread status.
func parseStatus(s string) (git.CommentThreadStatus, error) {
	names := make([]string, len(statuses))
	for i, st := range statuses {
		if strings.EqualFold(s, string(st)) {
			return st, nil
		}
		names[i] = string(st)
	}
	return "", cmdutil.FlagErrorf("invalid value for --status: %q (valid values: %s)", s, strings.Join(names, ", "))
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	prURL := shared.PRURL(shared.RepoURL(repo), *pr)
	if o.web {
		return cmdutil.OpenInBrowser(f, prURL)
	}

	id := shared.Deref(pr.PullRequestId)
	if o.editLast || o.deleteLast {
		last, err := lastComment(ctx, f, c, repo.Host, repo.Org, id)
		if err != nil {
			return err
		}
		switch {
		case last != nil && o.deleteLast:
			return deleteLast(ctx, f, o, c, id, last)
		case last != nil:
			return editLast(ctx, f, o, c, id, last, prURL)
		}
		create := o.createIfNone
		if !create && o.editLast && ios.CanPrompt() {
			if create, err = f.Prompter.Confirm("No comments found. Create one?", true); err != nil {
				return err
			}
		}
		if !create {
			return errNoUserComments
		}
		// Fall through: a new thread, as without --edit-last.
	}

	// Resolve the file before the editor opens, so a typo doesn't lose the typed body.
	var tc *git.CommentThreadContext
	var prc *git.GitPullRequestCommentThreadContext
	if o.file != "" {
		if tc, prc, err = inlineContext(ctx, c, o, id); err != nil {
			return err
		}
	}

	var body string
	// A status change alone needs no body; -e still asks for one.
	if o.status == "" || o.body.Given() || o.body.Editor {
		if body, err = o.body.Read(f, ""); err != nil {
			return err
		}
	}
	if o.thread == 0 {
		thread, err := c.CreateThread(ctx, id, body, tc, prc)
		if err != nil {
			return err
		}
		fmt.Fprintln(ios.Out, threadURL(prURL, shared.Deref(thread.Id)))
		return nil
	}

	url := threadURL(prURL, o.thread)
	if body != "" {
		thread, err := c.Thread(ctx, id, o.thread)
		if err != nil {
			return threadErr(err, o.thread, id)
		}
		if _, err := c.CreateComment(ctx, id, o.thread, rootComment(thread), body); err != nil {
			return err
		}
	}
	if o.status != "" {
		if _, err := c.UpdateThread(ctx, id, o.thread, o.status); err != nil {
			if body != "" {
				return fmt.Errorf("replied to thread %d (%s), but setting its status to %s failed: %w", o.thread, url, o.status, err)
			}
			return threadErr(err, o.thread, id)
		}
	}
	fmt.Fprintln(ios.Out, url)
	return nil
}

// threadURL is the web link to thread threadID of the pull request at prURL.
func threadURL(prURL string, threadID int) string {
	return fmt.Sprintf("%s?discussionId=%d", prURL, threadID)
}

// threadErr names the thread when err is a 404.
func threadErr(err error, threadID, id int) error {
	if api.IsNotFound(err) {
		return fmt.Errorf("thread %d not found in pull request !%d", threadID, id)
	}
	return err
}

// rootComment returns the ID of thread's first comment (the lowest ID), or 0 when it has none.
func rootComment(thread *git.GitPullRequestCommentThread) int {
	root := 0
	if thread.Comments == nil {
		return root
	}
	for _, cm := range *thread.Comments {
		if n := shared.Deref(cm.Id); n > 0 && (root == 0 || n < root) {
			root = n
		}
	}
	return root
}

// userComment is a comment and the ID of its thread.
type userComment struct {
	thread  int
	comment git.Comment
}

// lastComment returns the signed-in user's most recently published comment on pull request id,
// nil when there is none. Deleted and system comments and system threads don't count.
func lastComment(ctx context.Context, f *cmdutil.Factory, c *api.PRClient, host, org string, id int) (*userComment, error) {
	hc, err := authshared.HTTPClient(ctx, f, host, org)
	if err != nil {
		return nil, err
	}
	r := &api.IdentityResolver{HTTP: hc, OrgURL: authshared.OrgURL(host, org)}
	ids, err := r.Resolve(ctx, []string{"@me"})
	if err != nil {
		return nil, err
	}
	me := ids[0].String()
	all, err := c.Threads(ctx, id)
	if err != nil {
		return nil, err
	}
	var last *userComment
	for _, t := range shared.VisibleThreads(all) {
		for _, cm := range *t.Comments {
			if cm.Author == nil || !strings.EqualFold(shared.Deref(cm.Author.Id), me) {
				continue
			}
			if last == nil || published(cm).After(published(last.comment)) {
				last = &userComment{thread: shared.Deref(t.Id), comment: cm}
			}
		}
	}
	return last, nil
}

func published(c git.Comment) time.Time {
	if c.PublishedDate == nil {
		return time.Time{}
	}
	return c.PublishedDate.Time
}

// editLast replaces the content of last. Without body flags on a terminal, the editor opens on
// the current content and the result is confirmed before saving.
func editLast(ctx context.Context, f *cmdutil.Factory, o *options, c *api.PRClient, id int, last *userComment, prURL string) error {
	ios := f.IOStreams
	interactive := !o.body.Given() && !o.body.Editor && ios.CanPrompt()
	body, err := o.body.Read(f, shared.Deref(last.comment.Content))
	if err != nil {
		return err
	}
	if interactive {
		ok, err := f.Prompter.Confirm("Submit?", true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(ios.ErrOut, "Discarding...")
			return cmdutil.CancelError
		}
	}
	if _, err := c.UpdateComment(ctx, id, last.thread, shared.Deref(last.comment.Id), body); err != nil {
		return err
	}
	fmt.Fprintln(ios.Out, threadURL(prURL, last.thread))
	return nil
}

// deleteLast deletes last after a confirmation, unless --yes.
func deleteLast(ctx context.Context, f *cmdutil.Factory, o *options, c *api.PRClient, id int, last *userComment) error {
	ios := f.IOStreams
	if !o.yes {
		fmt.Fprintln(ios.ErrOut, "! Deleted comments cannot be recovered.")
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Delete the comment: %q?", preview(shared.Deref(last.comment.Content))), true)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}
	if err := c.DeleteComment(ctx, id, last.thread, shared.Deref(last.comment.Id)); err != nil {
		return err
	}
	fmt.Fprintln(ios.ErrOut, "Comment deleted")
	return nil
}

// preview shortens s to 40 characters for the delete prompt.
func preview(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}
