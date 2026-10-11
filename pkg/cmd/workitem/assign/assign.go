// Package assign implements `ado workitem assign`.
package assign

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	refs     []string
	to       string
	unassign bool
}

// NewCmdAssign returns the `workitem assign` command.
func NewCmdAssign(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "assign <id>...",
		Short: "Assign work items to a user",
		Long: `Assign one or more work items to a user, by default yourself.

--to takes an email, a display name or "@me". --unassign clears the assignee.`,
		Example: `  ado workitem assign 123
  ado wi assign 123 124 --to jane@example.com
  ado wi assign 123 --unassign`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.refs = args
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.to, "to", "@me", `Assign to a user (email, name or "@me")`)
	cmd.Flags().BoolVar(&o.unassign, "unassign", false, "Clear the assignee")
	cmd.MarkFlagsMutuallyExclusive("to", "unassign")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	ids, project, err := parseRefs(f, o.refs)
	if err != nil {
		return err
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(project.Host, project.Org)
	path := "/fields/System.AssignedTo"
	op := webapi.JsonPatchOperation{Op: &webapi.OperationValues.Remove, Path: &path}
	var name string
	if !o.unassign {
		resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
		u, err := resolver.ResolveUser(ctx, o.to)
		if err != nil {
			return err
		}
		op = webapi.JsonPatchOperation{Op: &webapi.OperationValues.Add, Path: &path, Value: u.FieldValue()}
		name = u.DisplayName
	}

	c := api.NewWorkItemClient(hc, orgURL, project.Project)
	ios := f.IOStreams
	cs := ios.ColorScheme()
	failed := false
	for _, id := range ids {
		if _, err := c.UpdateWorkItem(ctx, id, []webapi.JsonPatchOperation{op}); err != nil {
			fmt.Fprintf(ios.ErrOut, "%s Failed to update #%d: %v\n", cs.Red("X"), id, err)
			failed = true
			continue
		}
		switch {
		case !ios.IsStdoutTTY():
		case o.unassign:
			fmt.Fprintf(ios.ErrOut, "%s Unassigned #%d\n", cs.Green("✓"), id)
		default:
			fmt.Fprintf(ios.ErrOut, "%s Assigned #%d to %s\n", cs.Green("✓"), id, name)
		}
	}
	if failed {
		return cmdutil.SilentError
	}
	return nil
}

// parseRefs returns the IDs of refs and their project: the one a URL names, else the current
// one. All refs must be in one project.
func parseRefs(f *cmdutil.Factory, refs []string) ([]int, adocontext.Repo, error) {
	ids := make([]int, len(refs))
	var project adocontext.Repo
	for i, ref := range refs {
		id, p, err := shared.ParseRef(ref)
		if err != nil {
			return nil, adocontext.Repo{}, err
		}
		if p.Project == "" {
			if p, err = f.BaseProject(); err != nil {
				return nil, adocontext.Repo{}, err
			}
		}
		if i > 0 && (!strings.EqualFold(p.Host, project.Host) || !strings.EqualFold(p.Org, project.Org) ||
			!strings.EqualFold(p.Project, project.Project)) {
			return nil, adocontext.Repo{}, errors.New("all work items must be in the same project")
		}
		ids[i], project = id, p
	}
	return ids, project, nil
}
