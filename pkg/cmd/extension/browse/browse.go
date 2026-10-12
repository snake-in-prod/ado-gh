// Package browse implements `ado extension browse`.
package browse

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/markdown"
)

// NewCmdBrowse returns `ado extension browse`.
func NewCmdBrowse(f *cmdutil.Factory) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "browse",
		Short: "Browse extensions on GitHub interactively",
		Long: `Pick an extension from the GitHub repositories with the ado-extension topic whose name
starts with ado-, read its README, then install or remove it, or open it in your browser.
Installed extensions are marked with ✓.

It needs a terminal; use "ado extension search" in scripts. GH_TOKEN or GITHUB_TOKEN, when
set, is sent to api.github.com only, which raises GitHub's search rate limit.`,
		Example: `  ado extension browse
  ado extension browse --limit 300`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !f.IOStreams.CanPrompt() {
				return errors.New("browse requires a terminal; use 'ado extension search'")
			}
			if limit < 1 || limit > 1000 {
				return cmdutil.FlagErrorf("--limit must be between 1 and 1000")
			}
			return run(cmd, f, limit)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", 100, "Maximum number of repositories to search")
	return cmd
}

func run(cmd *cobra.Command, f *cmdutil.Factory, limit int) error {
	ios := f.IOStreams
	cs := ios.ColorScheme()
	hc, err := extpkg.GitHubClient(f)
	if err != nil {
		return err
	}
	repos, err := extpkg.SearchBrowsable(cmd.Context(), hc, limit)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return errors.New("no extensions found on GitHub (topic ado-extension)")
	}
	readmes := map[string]string{}
	for {
		installed, err := extpkg.InstalledSources(extpkg.Dir())
		if err != nil {
			return err
		}
		options := make([]string, 0, len(repos)+1)
		for _, r := range repos {
			mark := "  "
			if installed[r.Source.String()] {
				mark = "✓ "
			}
			options = append(options, fmt.Sprintf("%s%s/%s  ★ %d  %s", mark, r.Source.Owner, r.Source.Repo, r.Stars, tableprinter.OneLine(r.Description)))
		}
		i, err := f.Prompter.Select("Select an extension:", append(options, "Quit"))
		if err != nil || i == len(repos) {
			return err
		}
		r := repos[i]
		full := r.Source.Owner + "/" + r.Source.Repo
		md, cached := readmes[full]
		if !cached {
			if md, err = extpkg.FetchReadme(cmd.Context(), hc, r.Source); err != nil {
				return err
			}
			readmes[full] = md
		}
		if md == "" {
			md = "_" + full + " has no README._"
		}
		rendered, err := markdown.Render(ios, extpkg.Sanitize(md))
		if err != nil {
			return err
		}
		fmt.Fprintln(ios.Out, rendered)

		toggle, toggleArgs := "Install", []string{"install", full}
		if installed[r.Source.String()] {
			toggle, toggleArgs = "Remove", []string{"remove", r.Name}
		}
		action, err := f.Prompter.Select(full+":", []string{toggle, "Open in browser", "Back"})
		switch {
		case err != nil:
			return err
		case action == 0:
			err = runSibling(cmd, toggleArgs...)
		case action == 1:
			err = cmdutil.OpenInBrowser(f, r.URL())
		}
		if errors.Is(err, cmdutil.CancelError) {
			return err
		}
		if err != nil {
			fmt.Fprintf(ios.ErrOut, "%s %v\n", cs.Red("X"), err)
		}
	}
}

// runSibling runs the `extension` subcommand args[0] (install, remove) with the remaining
// positional args, as if typed, with its flags at their defaults.
func runSibling(cmd *cobra.Command, args ...string) error {
	c, rest, err := cmd.Parent().Find(args)
	if err != nil {
		return err
	}
	c.SetContext(cmd.Context())
	return c.RunE(c, rest)
}
