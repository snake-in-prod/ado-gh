package diff

import (
	"context"
	"fmt"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref                  string
	color                string
	nameOnly, patch, web bool
}

// NewCmdDiff returns the `pr diff` command.
func NewCmdDiff(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "diff [<number> | <url> | <branch>]",
		Short: "View changes in a pull request",
		Long: `Show the changes of a pull request as a unified diff, like its Files tab: from the merge
base of the target and source branches to the latest source commit.

Inside a clone of the pull request's repository the diff comes from git, after fetching the
source and target branches. Elsewhere (for example with -R) it is rebuilt from the REST API.

Without an argument, the pull request of the current branch is shown.`,
		Example: `  ado pr diff 123
  ado pr diff 123 --name-only
  ado pr diff --color never | less -R
  ado pr diff 123 -R org/project/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			if _, err := useColor(f.IOStreams, o.color); err != nil {
				return cmdutil.FlagErrorf("%s", err)
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.color, "color", "auto", "Use color in diff output: {auto|always|never}")
	cmd.Flags().BoolVar(&o.nameOnly, "name-only", false, "Display only names of changed files")
	cmd.Flags().BoolVar(&o.patch, "patch", false, "Display diff in patch format (needs a local clone)")
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the pull request diff in the browser")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	if o.web {
		u := shared.PRURL(shared.RepoURL(repo), *pr) + "?_a=files"
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "Opening %s in your browser.\n", u)
		}
		return f.Browser.Browse(u)
	}
	color, _ := useColor(ios, o.color)
	// The clone path needs the cwd to be a clone with a remote for the PR's repository; not
	// being in a git repository at all (no remotes) means the API path.
	if remotes, err := f.GitClient.Remotes(ctx); err == nil && shared.MatchRemote(remotes, repo) != "" {
		return cloneDiff(ctx, f, c, repo, pr, o.nameOnly, o.patch, color)
	}
	return apiDiff(ctx, f, c, pr, o.nameOnly, o.patch, color)
}
