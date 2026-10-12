// Package root builds the top-level ado command.
package root

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	extensionCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/extension"
	pipelineCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline"
	prCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/pr"
	releaseCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/release"
	repoCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/repo"
	sprintCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/sprint"
	versionCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/version"
	workitemCmd "github.com/snake-in-prod/ado-gh/pkg/cmd/workitem"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/extension"
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
	cmd.SetVersionTemplate(versionCmd.Text(f))
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
	cmd.AddCommand(newCmdCompletion(), aliasCmd.NewCmdAlias(f), apiCmd.NewCmdAPI(f), authCmd.NewCmdAuth(f), boardCmd.NewCmdBoard(f), browseCmd.NewCmdBrowse(f), configCmd.NewCmdConfig(f), extensionCmd.NewCmdExtension(f), pipelineCmd.NewCmdPipeline(f), prCmd.NewCmdPR(f), releaseCmd.NewCmdRelease(f), repoCmd.NewCmdRepo(f), sprintCmd.NewCmdSprint(f), versionCmd.NewCmdVersion(f), workitemCmd.NewCmdWorkItem(f))
	addAliasCommands(cmd, f)
	addExtensionCommands(cmd, f)

	// PATH extensions are listed only in root help and completion: scanning PATH is slow
	// (WSL /mnt/c entries), so a normal run looks up just the name it was given.
	help := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if c == cmd {
			addPathExtensions(cmd, f)
		}
		help(c, args)
	})
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var comps []cobra.Completion
		if len(args) == 0 {
			for _, name := range addPathExtensions(cmd, f) {
				if strings.HasPrefix(name, toComplete) {
					comps = append(comps, cobra.CompletionWithDesc(name, "Extension "+name))
				}
			}
		}
		return comps, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// AddPathExtension registers ado-<name> from PATH unless a built-in, alias or installed extension
// takes the name, the last step of the dispatch order. It reports whether it did.
func AddPathExtension(root *cobra.Command, f *cmdutil.Factory, name string) bool {
	if c, _, err := root.Find([]string{name}); (err == nil && c != root) || name == "help" || extension.ValidateName(name) != nil {
		return false
	}
	path, err := exec.LookPath(extension.Prefix + name) // refuses the current directory (ErrDot)
	if err != nil {
		return false
	}
	addExtension(root, name, func(args []string) error {
		return extension.RunPath(f, name, path, args)
	})
	return true
}

// addPathExtensions scans PATH and registers every ado-<name> executable AddPathExtension
// accepts, returning their names.
func addPathExtensions(root *cobra.Command, f *cmdutil.Factory) []string {
	var added []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			name, ok := strings.CutPrefix(e.Name(), extension.Prefix)
			if runtime.GOOS == "windows" {
				name = strings.TrimSuffix(name, filepath.Ext(name)) // LookPath adds PATHEXT back
			}
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			if AddPathExtension(root, f, name) {
				added = append(added, name)
			}
		}
	}
	return added
}

// addExtension adds an extension command, and the extension group with the first one: cobra
// panics on a GroupID without its group, and an unused group would print an empty heading.
func addExtension(root *cobra.Command, name string, run func(args []string) error) {
	if !root.ContainsGroup(aliasShared.ExtensionGroupID) {
		root.AddGroup(&cobra.Group{ID: aliasShared.ExtensionGroupID, Title: "Extension commands:"})
	}
	root.AddCommand(&cobra.Command{
		Use:                name,
		Short:              "Extension " + name,
		GroupID:            aliasShared.ExtensionGroupID,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return run(args)
		},
	})
}

// addExtensionCommands registers the installed extensions after the built-ins and aliases, so a
// name taken by either is skipped: built-in > alias > installed extension. Duplicate cobra
// names would resolve nondeterministically.
func addExtensionCommands(root *cobra.Command, f *cmdutil.Factory) {
	exts, err := extension.List(extension.Dir())
	if err != nil {
		return // `ado extension list` reports it
	}
	for _, ext := range exts {
		if c, _, err := root.Find([]string{ext.Name}); (err == nil && c != root) || ext.Name == "help" {
			continue
		}
		addExtension(root, ext.Name, func(args []string) error {
			return extension.Run(f, ext, args)
		})
	}
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
