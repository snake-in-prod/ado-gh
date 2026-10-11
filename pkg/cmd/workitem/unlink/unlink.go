// Package unlink implements `ado workitem unlink`.
package unlink

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdUnlink returns the `workitem unlink` command.
func NewCmdUnlink(f *cmdutil.Factory) *cobra.Command {
	var links shared.LinkFlags
	cmd := &cobra.Command{
		Use:   "unlink <id>",
		Short: "Remove links from a work item",
		Long: `Remove links from a work item. <id> is an ID, #ID or a work item URL.

Takes the same flags as 'ado workitem link'. Each must match an existing link,
else nothing is removed.`,
		Example: `  ado workitem unlink 123 --parent 100
  ado wi unlink 123 --pr 45 --related 7`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), f, args[0], &links)
		},
	}
	links.Register(cmd.Flags())
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, ref string, flags *shared.LinkFlags) error {
	t, err := shared.NewLinkTarget(ctx, f, ref)
	if err != nil {
		return err
	}
	links, err := flags.Parse(ctx, t.Parser)
	if err != nil {
		return err
	}
	c := t.Parser.Client
	wi, err := c.WorkItem(ctx, t.ID, true)
	if err != nil {
		return err
	}
	removes, err := shared.RemoveOps(t.ID, wi.Relations, links)
	if err != nil {
		return err
	}
	// The rev test fails the patch if the work item changed since it was read, which would
	// make the relation indexes stale.
	test, path := webapi.OperationValues.Test, "/rev"
	ops := append([]webapi.JsonPatchOperation{{Op: &test, Path: &path, Value: wi.Rev}}, removes...)
	if _, err := c.UpdateWorkItem(ctx, t.ID, ops); err != nil {
		if api.IsRevConflict(err) {
			return fmt.Errorf("work item %d changed since read; retry", t.ID)
		}
		return err
	}
	fmt.Fprintln(f.IOStreams.Out, adocontext.WorkItemURL(t.Project, t.ID))
	return nil
}
