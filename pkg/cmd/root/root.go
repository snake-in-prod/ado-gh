// Package root builds the top-level ado command.
package root

import (
	apiCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/api"
	authCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/auth"
	boardCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/board"
	browseCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/browse"
	configCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/config"
	prCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/pr"
	repoCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/repo"
	sprintCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/sprint"
	workitemCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/workitem"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRoot returns the ado root command.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ado <command> <subcommand> [flags]",
		Short:         "Azure DevOps CLI",
		Long:          "Work seamlessly with Azure DevOps from the command line.",
		Version:       f.AppVersion,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetIn(f.IOStreams.In)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)
	cmd.SetVersionTemplate("ado version {{.Version}}\n")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return cmdutil.FlagErrorWrap(err)
	})
	cmd.PersistentFlags().BoolVar(&f.Debug, "debug", f.Debug, "Log HTTP requests to stderr (also ADO_DEBUG=1)")
	cmd.PersistentFlags().StringVarP(&f.RepoOverride, "repo", "R", "",
		"Select another repository using the [ORG/]PROJECT/REPO format (also ADO_REPO)")

	cmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "additional", Title: "Additional commands:"},
	)
	cmd.SetHelpCommandGroupID("additional")
	cmd.AddCommand(newCmdCompletion(), apiCmd.NewCmdAPI(f), authCmd.NewCmdAuth(f), boardCmd.NewCmdBoard(f), browseCmd.NewCmdBrowse(f), configCmd.NewCmdConfig(f), prCmd.NewCmdPR(f), repoCmd.NewCmdRepo(f), sprintCmd.NewCmdSprint(f), workitemCmd.NewCmdWorkItem(f))
	return cmd
}

// newCmdCompletion is explicit because cobra adds its default only when other
// subcommands exist.
func newCmdCompletion() *cobra.Command {
	return &cobra.Command{
		Use:       "completion {bash|zsh|fish|powershell}",
		Short:     "Generate shell completion scripts",
		GroupID:   "additional",
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, out := cmd.Root(), cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			default:
				return root.GenPowerShellCompletionWithDesc(out)
			}
		},
	}
}
