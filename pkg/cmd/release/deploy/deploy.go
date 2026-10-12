// Package deploy implements `ado release deploy` and `ado release cancel`.
package deploy

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref, environment, comment string
	yes, cancel               bool
}

// NewCmdDeploy returns the `release deploy` command.
func NewCmdDeploy(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, &options{}, &cobra.Command{
		Use:   "deploy <release> --environment <name>",
		Short: "Deploy a release to an environment",
		Long: `Start a deployment of a release to one of its environments: a manual environment, or a
redeployment of one already deployed. Pre-deployment approvals still apply.

<release> is a release ID or URL; --environment is an environment name or ID.

On a terminal, ado asks for confirmation; --yes skips that and is required when not
running interactively.`,
		Example: `  ado release deploy 45 --environment Prod
  ado release deploy 45 --environment Prod -c "hotfix" --yes`,
	})
}

// NewCmdCancel returns the `release cancel` command.
func NewCmdCancel(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, &options{cancel: true}, &cobra.Command{
		Use:   "cancel <release> --environment <name>",
		Short: "Cancel the deployment of a release to an environment",
		Long: `Cancel the in-progress deployment of a release to one of its environments.

<release> is a release ID or URL; --environment is an environment name or ID.

On a terminal, ado asks for confirmation; --yes skips that and is required when not
running interactively.`,
		Example: `  ado release cancel 45 --environment Prod
  ado release cancel 45 --environment Prod --yes`,
	})
}

func newCmd(f *cmdutil.Factory, o *options, cmd *cobra.Command) *cobra.Command {
	cmd.Args = cobra.ExactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		o.ref = args[0]
		if o.environment == "" {
			return cmdutil.FlagErrorf("--environment is required")
		}
		if !o.yes && !f.IOStreams.CanPrompt() {
			return cmdutil.FlagErrorf("--yes required when not running interactively")
		}
		return run(cmd.Context(), f, o)
	}
	fl := cmd.Flags()
	fl.StringVar(&o.environment, "environment", "", "Environment `name` or ID (required)")
	fl.StringVarP(&o.comment, "comment", "c", "", "Comment to record with the request")
	fl.BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	id, project, err := shared.ParseReleaseRef(o.ref)
	if err != nil {
		return err
	}
	if project.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return err
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	r, err := c.ReleaseByID(ctx, id)
	if err != nil {
		return err
	}
	tree := shared.NewTree(*r)
	i, err := tree.EnvironmentIndex(o.environment, id)
	if err != nil {
		return err
	}
	env, name := tree.Environments[i], prshared.Deref(r.Name)

	status := release.EnvironmentStatusValues.InProgress
	question := fmt.Sprintf("Deploy %s to %s?", name, env.Name)
	done := fmt.Sprintf("Started deployment of %s to %s", name, env.Name)
	if o.cancel {
		switch env.Status {
		case "inProgress", "queued", "scheduled":
		default:
			return fmt.Errorf("%s is not deploying to %s (status %s)", name, env.Name, env.Status)
		}
		status = release.EnvironmentStatusValues.Canceled
		question = fmt.Sprintf("Cancel the deployment of %s to %s?", name, env.Name)
		done = fmt.Sprintf("Canceled the deployment of %s to %s", name, env.Name)
	}
	if !o.yes {
		ok, err := f.Prompter.Confirm(question, false)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}

	if _, err := c.UpdateEnvironment(ctx, id, env.ID, status, o.comment); err != nil {
		return err
	}
	ios := f.IOStreams
	fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().Green("✓"), done)
	return nil
}
