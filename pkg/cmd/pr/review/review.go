// Package review implements `ado pr review`.
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// kind is one review action: a vote, or a comment without a vote.
type kind struct {
	flag, short, usage, label string
	vote                      int
	done                      string // success message, %d = pull request id
}

var kinds = []kind{
	{"approve", "a", "Approve the pull request (vote 10)", "Approve", 10, "Approved pull request !%d"},
	{"approve-with-suggestions", "", "Approve with suggestions (vote 5)", "Approve with suggestions", 5, "Approved pull request !%d with suggestions"},
	{"wait", "", "Wait for author (vote -5)", "Wait for author", -5, "Voted wait for author on pull request !%d"},
	{"reject", "", "Reject the pull request (vote -10)", "Reject", -10, "Rejected pull request !%d"},
	{"reset", "", "Reset your vote (vote 0)", "Reset vote", 0, "Reset your vote on pull request !%d"},
	{"comment", "c", "Add a comment thread without voting (requires --body)", "Comment", 0, "Commented on pull request !%d"},
}

const (
	waitIdx    = 2
	commentIdx = 5
)

// NewCmdReview returns the `pr review` command.
func NewCmdReview(f *cmdutil.Factory) *cobra.Command {
	set := make([]bool, len(kinds))
	var requestChanges bool
	var body string
	cmd := &cobra.Command{
		Use:   "review [<number> | <url> | <branch>]",
		Short: "Vote on or comment on a pull request",
		Long: `Vote on a pull request, or add a comment thread to it.

Without an argument, the pull request of the current branch is reviewed. With --body, a vote
also adds the text as a comment thread. Without flags, ado asks interactively.`,
		Example: `  ado pr review 123 --approve
  ado pr review --wait --body "Please add tests"
  ado pr review 123 --comment --body "Looks good overall"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chosen, n := -1, 0
			for i, on := range set {
				if on {
					chosen, n = i, n+1
				}
			}
			if requestChanges {
				chosen, n = waitIdx, n+1
			}
			switch {
			case n > 1:
				return cmdutil.FlagErrorf("only one of --approve, --approve-with-suggestions, --wait/--request-changes, --reject, --reset or --comment may be used")
			case chosen == commentIdx && strings.TrimSpace(body) == "":
				return cmdutil.FlagErrorf("--body is required with --comment")
			case n == 0 && !f.IOStreams.CanPrompt():
				return cmdutil.FlagErrorf("--approve, --approve-with-suggestions, --wait, --reject, --reset or --comment required when not running interactively")
			}
			ref := ""
			if len(args) > 0 {
				ref = args[0]
			}
			return run(cmd.Context(), f, ref, chosen, body)
		},
	}
	fl := cmd.Flags()
	for i, k := range kinds {
		fl.BoolVarP(&set[i], k.flag, k.short, false, k.usage)
	}
	fl.BoolVarP(&requestChanges, "request-changes", "r", false, "Alias of --wait")
	fl.StringVarP(&body, "body", "b", "", "Comment text, added as a comment thread")
	return cmd
}

// run applies kinds[chosen] to the pull request ref; chosen < 0 asks for it.
func run(ctx context.Context, f *cmdutil.Factory, ref string, chosen int, body string) error {
	pr, c, repo, err := shared.Find(ctx, f, ref)
	if err != nil {
		return err
	}
	if chosen < 0 {
		if chosen, body, err = ask(f.Prompter, body); err != nil {
			return err
		}
	}
	id, k := *pr.PullRequestId, kinds[chosen]

	if chosen != commentIdx {
		hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
		if err != nil {
			return err
		}
		resolver := &api.IdentityResolver{HTTP: hc, OrgURL: authshared.OrgURL(repo.Host, repo.Org)}
		me, err := resolver.Resolve(ctx, []string{"@me"})
		if err != nil {
			return err
		}
		if _, err := c.Vote(ctx, id, me[0].String(), k.vote); err != nil {
			return err
		}
	}
	if strings.TrimSpace(body) != "" {
		if _, err := c.CreateThread(ctx, id, body); err != nil {
			return err
		}
	}
	if ios := f.IOStreams; ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s "+k.done+"\n", ios.ColorScheme().Green("✓"), id)
	}
	return nil
}

// ask prompts for the review kind and, unless body is set, its comment.
func ask(p *cmdutil.Prompter, body string) (int, string, error) {
	labels := make([]string, len(kinds))
	for i, k := range kinds {
		labels[i] = k.label
	}
	chosen, err := p.Select("What kind of review do you want to give?", labels)
	if err != nil || body != "" {
		return chosen, body, err
	}
	prompt := "Comment (optional)"
	if chosen == commentIdx {
		prompt = "Comment"
	}
	if body, err = p.Input(prompt, ""); err != nil {
		return 0, "", err
	}
	if chosen == commentIdx && strings.TrimSpace(body) == "" {
		return 0, "", errors.New("comment cannot be blank")
	}
	return chosen, body, nil
}
