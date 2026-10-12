// Package upgrade implements `ado extension upgrade`.
package upgrade

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
)

// NewCmdUpgrade returns `ado extension upgrade [<name>] [--all]`.
func NewCmdUpgrade(f *cmdutil.Factory) *cobra.Command {
	var all, force, dryRun bool
	var trust extpkg.Trust
	cmd := &cobra.Command{
		Use:   "upgrade [<name>]",
		Short: "Upgrade installed extensions",
		Long: `Upgrade an installed extension, or all of them with --all, to its latest version.

A binary extension gets the latest GitHub release or Azure Artifacts package version, with
the same checksum rules as install. A script extension is fast-forwarded to the remote's
default branch (git fetch origin HEAD, then git merge --ff-only FETCH_HEAD); when its history
no longer fast-forwards, --force resets it to the remote, discarding local changes. Local
extensions are skipped, and pinned ones unless --force, which also unpins them.

The recorded source must still be allowed by extension_sources. Each upgrade is confirmed as
an install is; --yes skips that and is required when not running interactively. Token consent
given before is kept; an extension that requests your Azure DevOps token without it asks
again (or takes --allow-token).

--dry-run shows what would change ("name: v1 → v2") without changing anything. With --all, a
failure does not stop the other upgrades; the command then exits 1 and names the failures.`,
		Example: `  ado extension upgrade lint
  ado extension upgrade --all --dry-run
  ado extension upgrade --all --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return cmdutil.FlagErrorf("specify an extension to upgrade or use --all")
			}
			if len(args) > 0 && all {
				return cmdutil.FlagErrorf("cannot use --all with an extension name")
			}
			ios := f.IOStreams
			root := extpkg.Dir()
			names := args
			if all {
				exts, err := extpkg.List(root)
				if err != nil {
					return err
				}
				if len(exts) == 0 {
					if ios.IsStderrTTY() {
						fmt.Fprintln(ios.ErrOut, "no installed extensions found")
					}
					return nil
				}
				for _, e := range exts {
					names = append(names, e.Name)
				}
			} else if _, err := extpkg.Lookup(root, args[0]); err != nil {
				return err
			}
			if !dryRun {
				if err := trust.Check(ios); err != nil {
					return err
				}
			}
			var failed []string
			for _, name := range names {
				res, err := extpkg.Upgrade(cmd.Context(), f, extpkg.UpgradeOptions{Root: root, Name: name,
					Force: force, DryRun: dryRun, Trust: trust})
				switch {
				case err == nil && res.Skipped != "":
					fmt.Fprintf(ios.Out, "%s: %s\n", name, res.Skipped)
				case err == nil && dryRun:
					fmt.Fprintf(ios.Out, "%s: %s → %s\n", name, res.From, res.To)
				case err == nil:
					fmt.Fprintf(ios.Out, "%s: upgraded %s → %s\n", name, res.From, res.To)
				case !all:
					return err
				case errors.Is(err, cmdutil.CancelError):
					fmt.Fprintf(ios.Out, "%s: skipped, upgrade cancelled\n", name)
				default:
					fmt.Fprintf(ios.ErrOut, "%s: %v\n", name, err)
					failed = append(failed, name)
				}
			}
			if len(failed) > 0 {
				fmt.Fprintf(ios.ErrOut, "Failed to upgrade %d of %d extensions: %s\n", len(failed), len(names), strings.Join(failed, ", "))
				return cmdutil.SilentError
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Upgrade all installed extensions")
	cmd.Flags().BoolVar(&force, "force", false, "Upgrade pinned extensions and reset script extensions whose history diverged")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show available upgrades without installing them")
	cmd.Flags().BoolVarP(&trust.Yes, "yes", "y", false, "Skip the upgrade confirmation")
	cmd.Flags().BoolVar(&trust.AllowToken, "allow-token", false, "Give an extension your Azure DevOps token if it requests it")
	cmd.Flags().BoolVar(&trust.AllowUnverified, "allow-unverified", false, "Install a release binary that has no checksums file")
	return cmd
}
