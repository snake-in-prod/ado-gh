// Package enable implements `ado pipeline enable` and `ado pipeline disable`.
package enable

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdEnable returns the `pipeline enable` command.
func NewCmdEnable(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, build.DefinitionQueueStatusValues.Enabled, "enable", "Enable a pipeline",
		`Enable a pipeline: set its queue status to enabled, so runs can be queued and start.
A paused pipeline is resumed too.`)
}

// NewCmdDisable returns the `pipeline disable` command.
func NewCmdDisable(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, build.DefinitionQueueStatusValues.Disabled, "disable", "Disable a pipeline",
		`Disable a pipeline: set its queue status to disabled, so no run can be queued, by
users or by triggers, and queued runs do not start.`)
}

// newCmd returns the command verb, which sets a pipeline's queue status to status.
func newCmd(f *cmdutil.Factory, status build.DefinitionQueueStatus, verb, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <pipeline>",
		Short: short,
		Long: long + `

<pipeline> is a pipeline ID, name, folder path (\Folder\Name or Folder/Name) or URL.
Nothing changes when the pipeline already has that status.`,
		Example: fmt.Sprintf("  ado pipeline %[1]s 12\n  ado pipeline %[1]s CI/ado-gh", verb),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSet(cmd.Context(), f, args[0], status)
		},
	}
}

func runSet(ctx context.Context, f *cmdutil.Factory, arg string, status build.DefinitionQueueStatus) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	ref, err := shared.ParsePipelineRef(arg)
	if err != nil {
		return err
	}
	if ref.Project.Project != "" {
		project = ref.Project
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	id, err := shared.ResolvePipeline(ctx, c, ref)
	if err != nil {
		return err
	}
	// SetQueueStatus already retried a revision conflict once.
	prev, err := c.SetQueueStatus(ctx, id, status)
	if api.IsDefinitionRevisionConflict(err) {
		return fmt.Errorf("pipeline %d was modified concurrently; run the command again (%w)", id, err)
	}
	if err != nil {
		return err
	}
	ios := f.IOStreams
	if prev == status {
		fmt.Fprintf(ios.ErrOut, "Pipeline %d is already %s\n", id, status)
		return nil
	}
	fmt.Fprintf(ios.ErrOut, "%s Pipeline %d is now %s (was %s)\n", ios.ColorScheme().Green("✓"), id, status, prev)
	return nil
}
