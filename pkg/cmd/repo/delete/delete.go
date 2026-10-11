// Package delete implements `ado repo delete`.
package delete

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	arg            string
	permanent, yes bool
}

// NewCmdDelete returns the `repo delete` command.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "delete [<repo>]",
		Short: "Delete a repository",
		Long: `Delete a repository. <repo> is REPO, PROJECT/REPO, ORG/PROJECT/REPO or a URL; the
default is the current repository.

The repository moves to the project's recycle bin, where it stays for 30 days and can be
restored with 'ado repo restore'. --permanent also purges it from the recycle bin, which
cannot be undone. Azure DevOps refuses to delete the only repository of a project.

On a terminal, ado asks you to type the repository name to confirm; --yes skips that.`,
		Example: `  ado repo delete old-svc
  ado repo delete myproject/old-svc --permanent --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.arg = strings.Join(args, "")
			if !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--yes required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVar(&o.permanent, "permanent", false, "Also purge the repository from the recycle bin (irreversible)")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	repo, err := shared.ParseRepo(f, o.arg)
	if err != nil {
		return err
	}
	client, err := shared.NewClient(ctx, f, repo)
	if err != nil {
		return err
	}
	// The delete APIs take the GUID; disabled repositories are left to the server to judge.
	r, err := client.Repository(ctx, repo.Name)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	fullName := r.Project + "/" + r.Name
	if !o.yes {
		prompt := fmt.Sprintf("Type %s to move %s to the recycle bin (restorable for 30 days):", r.Name, fullName)
		if o.permanent {
			prompt = fmt.Sprintf("Type %s to permanently delete %s (this cannot be undone):", r.Name, fullName)
		}
		typed, err := f.Prompter.Input(prompt, "")
		if err != nil {
			return err
		}
		if strings.TrimSpace(typed) != r.Name {
			fmt.Fprintln(ios.ErrOut, "The name did not match; nothing was deleted.")
			return cmdutil.CancelError
		}
	}

	if err := client.DeleteRepository(ctx, r.ID); err != nil {
		return err
	}
	// A bare name resolves against the same git context in `repo restore`; a target from an
	// argument path, -R or ADO_REPO is spelled out so the hint works when pasted elsewhere.
	ref := r.Name
	if strings.ContainsAny(o.arg, "/:") || !f.BaseRepoFromGit() {
		ref = repo.Org + "/" + r.Project + "/" + r.Name
	}
	restore := "ado repo restore " + shellQuote(ref)
	if !o.permanent {
		fmt.Fprintf(ios.ErrOut, "Deleted repository %s (restorable for 30 days: %s)\n", fullName, restore)
		return nil
	}
	if err := client.PurgeRepository(ctx, r.ID); err != nil {
		return fmt.Errorf("%s is still in the recycle bin: purging it failed: %w (restore it with: %s)", fullName, err, restore)
	}
	fmt.Fprintf(ios.ErrOut, "Permanently deleted repository %s\n", fullName)
	return nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// shellQuote single-quotes s for POSIX shells unless it only has [A-Za-z0-9._/-].
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
