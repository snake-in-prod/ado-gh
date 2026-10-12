// Package cancel implements `ado pipeline cancel`.
package cancel

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	run string
	yes bool
}

// NewCmdCancel returns the `pipeline cancel` command.
func NewCmdCancel(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "cancel [<run>]",
		Short: "Cancel a run",
		Long: `Cancel a run: its status becomes cancelling until its agents stop.

<run> is a run ID or URL. Without it, you are asked to pick one of the project's
in-progress runs.

On a terminal, ado asks for confirmation; --yes skips that and is required when not
running interactively. A completed run cannot be canceled.`,
		Example: `  ado pipeline cancel 812
  ado run cancel 812 --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.run = args[0]
			}
			if !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--yes required when not running interactively")
			}
			return runCancel(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func runCancel(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var id int
	if o.run != "" {
		var p adocontext.Repo
		if id, p, err = shared.ParseRunRef(o.run); err != nil {
			return err
		}
		if p.Project != "" {
			project = p
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	if id == 0 {
		status := build.BuildStatusValues.InProgress
		if id, err = shared.SelectRun(ctx, f, c, build.GetBuildsArgs{StatusFilter: &status}); err != nil {
			return err
		}
	}

	// The server's answer to cancelling a completed run is undocumented, so check first.
	b, err := c.BuildByID(ctx, id)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	switch prshared.Deref(b.Status) {
	case build.BuildStatusValues.Completed:
		return fmt.Errorf("run %d has already completed", id)
	case build.BuildStatusValues.Cancelling:
		fmt.Fprintf(ios.ErrOut, "Run %d is already being canceled\n", id)
		return nil
	}
	if !o.yes {
		var def string
		if b.Definition != nil {
			def = prshared.Deref(b.Definition.Name) + " "
		}
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Cancel run %d (%s%s)?", id, def, prshared.Deref(b.BuildNumber)), false)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}

	if b, err = c.CancelBuild(ctx, id); err != nil {
		return err
	}
	if b != nil && prshared.Deref(b.Status) == build.BuildStatusValues.Completed {
		return fmt.Errorf("run %d has already completed", id)
	}
	fmt.Fprintf(ios.ErrOut, "%s Requested cancellation of run %d\n", ios.ColorScheme().Green("✓"), id)
	return nil
}
