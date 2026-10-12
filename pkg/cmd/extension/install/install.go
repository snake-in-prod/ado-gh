// Package install implements `ado extension install`.
package install

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
)

// NewCmdInstall returns `ado extension install <source>`.
func NewCmdInstall(f *cmdutil.Factory) *cobra.Command {
	var force bool
	var pin string
	var trust extpkg.Trust
	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install an extension",
		Long: `Install an extension from a repository or a local directory named ado-<name>.

The source is OWNER/REPO (github.com), a GitHub or Azure DevOps repository URL, or a local
path. A repository is cloned with git; its root must hold an executable ado-<name> (git mode
100755; on Windows a script starting with #! or an ado-<name>.ps1/.cmd also works). A private
Azure DevOps repository is cloned over HTTPS with your ado login, which needs git 2.31 or
later; the token is passed in git's environment, never stored. Use --pin to install a tag or
commit instead of the default branch.

Before a repository install, ado shows its source, resolved tag or commit, kind, checksum
status and whether it requests your Azure DevOps token, and asks to confirm; --yes skips
that and is required when not running interactively. An extension whose ado-extension.yml
says "token: true" gets ADO_TOKEN only with your consent: a separate question (default no),
or --allow-token. Without consent it is installed anyway but may not work.

A local install links to the directory (a symlink; on Windows a file holding its path), so
changes there take effect immediately. It never gets an auth token. The extension_sources
config allowlist must include "local" when it is set.`,
		Example: `  ado extension install myorg/ado-lint
  ado extension install https://dev.azure.com/myorg/tools/_git/ado-lint --pin v1.2.0
  ado extension install myorg/ado-lint --yes --allow-token
  ado extension install .
  ado extension install ../ado-hello --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := extpkg.ParseSource(args[0])
			if err != nil {
				return err
			}
			if src.IsLocal() && pin != "" {
				return errors.New("local extensions cannot be pinned")
			}
			if err := extpkg.CheckSource(cmd.Context(), f, src); err != nil {
				return err
			}
			repo := src.Repo
			if src.IsLocal() {
				repo = filepath.Base(src.Path)
			}
			name, err := extpkg.NameFromRepo(repo)
			if err != nil {
				return err
			}
			if err := extpkg.CheckCollision(cmd.Root(), name); err != nil {
				return err
			}
			if src.IsLocal() {
				_, err = extpkg.InstallLocal(extpkg.Dir(), name, src.Path, force)
			} else {
				_, err = extpkg.InstallScript(cmd.Context(), f, extpkg.ScriptOptions{Root: extpkg.Dir(), Name: name,
					Source: src, URL: extpkg.CloneURL(args[0], src), Pin: pin, Force: force, Trust: trust})
			}
			ios := f.IOStreams
			cs := ios.ColorScheme()
			if errors.Is(err, extpkg.ErrExecutableNotFound) {
				fmt.Fprintf(ios.ErrOut, "%s Installed extension %s, but %s has no executable %s yet\n",
					cs.Yellow("!"), name, src.Path, extpkg.Prefix+name)
				return nil
			}
			if err != nil {
				return err
			}
			if ios.IsStderrTTY() {
				fmt.Fprintf(ios.ErrOut, "%s Installed extension %s\n", cs.Green("✓"), name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Replace an installed extension of the same name")
	cmd.Flags().StringVar(&pin, "pin", "", "Install a specific `tag or commit` of a repository extension")
	cmd.Flags().BoolVarP(&trust.Yes, "yes", "y", false, "Skip the install confirmation")
	cmd.Flags().BoolVar(&trust.AllowToken, "allow-token", false, "Give the extension your Azure DevOps token if it requests it")
	return cmd
}
