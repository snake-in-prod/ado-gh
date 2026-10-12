// Package root builds the top-level ado command.
package root

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	aliasCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/alias"
	aliasShared "github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	apiCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/api"
	authCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/auth"
	boardCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/board"
	browseCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/browse"
	configCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/config"
	pipelineCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline"
	prCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/pr"
	releaseCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/release"
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
	cmd.AddCommand(newCmdCompletion(), aliasCmd.NewCmdAlias(f), apiCmd.NewCmdAPI(f), authCmd.NewCmdAuth(f), boardCmd.NewCmdBoard(f), browseCmd.NewCmdBrowse(f), configCmd.NewCmdConfig(f), pipelineCmd.NewCmdPipeline(f), prCmd.NewCmdPR(f), releaseCmd.NewCmdRelease(f), repoCmd.NewCmdRepo(f), sprintCmd.NewCmdSprint(f), workitemCmd.NewCmdWorkItem(f))
	addAliasCommands(cmd, f)
	return cmd
}

// addAliasCommands registers the user's aliases as commands after the built-ins, so a name
// taken by a built-in, its cobra aliases or help is skipped: built-ins win. The alias group is
// added only when an alias is registered: cobra panics on a GroupID without its group, and an
// unused group would print an empty heading.
func addAliasCommands(root *cobra.Command, f *cmdutil.Factory) {
	cfg, err := f.Config()
	if err != nil {
		return // commands that read the config report it
	}
	ac, err := aliasShared.AliasConfig(cfg)
	if err != nil {
		return
	}
	aliases := ac.Aliases()
	for _, name := range slices.Sorted(maps.Keys(aliases)) {
		if name == "" || name == "help" || strings.HasPrefix(name, "-") || strings.ContainsFunc(name, unicode.IsSpace) {
			continue
		}
		if c, _, err := root.Find([]string{name}); err == nil && c != root {
			continue
		}
		if !root.ContainsGroup(aliasShared.AliasGroupID) {
			root.AddGroup(&cobra.Group{ID: aliasShared.AliasGroupID, Title: "Alias commands:"})
		}
		expansion := aliases[name]
		root.AddCommand(&cobra.Command{
			Use:                name,
			Short:              fmt.Sprintf("Alias for %q", expansion),
			GroupID:            aliasShared.AliasGroupID,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				if aliasShared.IsShell(expansion) {
					return aliasShared.RunShell(f.IOStreams, cfg.Get("", "alias_shell"), expansion, args)
				}
				// Checked against built-ins only, so an alias expands exactly once.
				if err := aliasShared.ValidateExpansion(root, expansion); err != nil {
					return fmt.Errorf("alias %q: %w", name, err)
				}
				expanded, err := aliasShared.Expand(expansion, args)
				if err != nil {
					return err
				}
				root.SetArgs(expanded)
				return root.Execute()
			},
		})
	}
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
