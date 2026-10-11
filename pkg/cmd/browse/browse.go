package browse

import (
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdBrowse returns the `ado browse` command.
func NewCmdBrowse(f *cmdutil.Factory) *cobra.Command {
	var o Options
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "browse [<number> | <path> | <commit-sha>]",
		Short: "Open the repository in the browser",
		Long: `Open the current repository, or a page of it, in the web browser.

The argument selects the page:
  !123 or 123       pull request 123
  #456              work item 456
  <commit-sha>      commit page (7 to 40 hex characters)
  <path>[:N[-M]]    file or folder, optionally highlighting line N or lines N to M

Inside a clone, paths are relative to the current directory; with -R they are relative to
the repository root. Without --branch or --commit, files open on the default branch.
--commit without a value is the local HEAD; pass a SHA as --commit=<sha>.

The section flags open project or repository pages and take no argument.

With --no-browser the URL is printed instead of opened.`,
		Example: `  ado browse
  ado browse pkg/api.go:42
  ado browse !123
  ado browse '#456'
  ado browse --branch dev README.md
  ado browse --commit=abc1234 main.go:10-20
  ado browse --wiki -n
  ado browse --pipelines -R myproject/api`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Target = strings.Join(args, "")
			u, err := BuildURL(cmd.Context(), f, o)
			if err != nil {
				return err
			}
			if noBrowser {
				_, err := fmt.Fprintln(f.IOStreams.Out, u)
				return err
			}
			return cmdutil.OpenInBrowser(f, u)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&noBrowser, "no-browser", "n", false, "Print the URL instead of opening the browser")
	fl.StringVarP(&o.Branch, "branch", "b", "", "Show files on `branch`")
	fl.StringVarP(&o.Commit, "commit", "c", "", "Show files at `sha` (default: the local HEAD)")
	fl.Lookup("commit").NoOptDefVal = LastCommit
	fl.BoolVarP(&o.Settings, "settings", "s", false, "Open the repository settings")
	fl.BoolVarP(&o.Wiki, "wiki", "w", false, "Open the project wiki")
	fl.BoolVar(&o.Pipelines, "pipelines", false, "Open the project pipelines")
	fl.BoolVar(&o.Boards, "boards", false, "Open the project boards")
	fl.BoolVar(&o.PRs, "prs", false, "Open the repository's pull request list")
	fl.BoolVar(&o.Branches, "branches", false, "Open the repository's branches")
	return cmd
}
