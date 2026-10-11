// Package restore implements `ado workitem restore`.
package restore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRestore returns the `workitem restore` command.
func NewCmdRestore(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>...",
		Short: "Restore deleted work items from the recycle bin",
		Long: `Restore work items that 'ado workitem delete' moved to the project's recycle bin.
Each <id> is an ID, #ID or a work item URL. Permanently deleted work items cannot be restored.

Restoring needs the project permission "Delete and restore work items".`,
		Example: `  ado workitem restore 123
  ado wi restore 123 124`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), f, args)
		},
	}
}

type item struct {
	id      int
	project adocontext.Repo
}

func run(ctx context.Context, f *cmdutil.Factory, refs []string) error {
	var items []item
	for _, ref := range refs {
		id, project, err := shared.ParseRef(ref)
		if err != nil {
			return err
		}
		if project.Project == "" {
			if project, err = f.BaseProject(); err != nil {
				return err
			}
		}
		items = append(items, item{id, project})
	}

	ios := f.IOStreams
	cs := ios.ColorScheme()
	clients := map[adocontext.Repo]*api.WorkItemClient{}
	var done, failed []string
	for _, it := range items {
		title, err := restore(ctx, f, clients, it)
		if err == nil {
			done = append(done, strconv.Itoa(it.id))
			if ios.IsStdoutTTY() {
				fmt.Fprintf(ios.ErrOut, "%s Restored work item %d (%s)\n", cs.Green("✓"), it.id, title)
			}
			fmt.Fprintln(ios.Out, adocontext.WorkItemURL(it.project, it.id))
			continue
		}
		if len(items) == 1 {
			return err
		}
		failed = append(failed, strconv.Itoa(it.id))
		fmt.Fprintf(ios.ErrOut, "%s Failed to restore work item %d: %v\n", cs.Red("X"), it.id, err)
	}
	if len(failed) == 0 {
		return nil
	}
	if len(done) == 0 {
		return fmt.Errorf("failed to restore work items %s", strings.Join(failed, ", "))
	}
	return fmt.Errorf("failed to restore work items %s; done: %s", strings.Join(failed, ", "), strings.Join(done, ", "))
}

// restore restores one item through its project's client, built once per project.
func restore(ctx context.Context, f *cmdutil.Factory, clients map[adocontext.Repo]*api.WorkItemClient, it item) (string, error) {
	p := it.project
	key := adocontext.Repo{Host: p.Host, Org: strings.ToLower(p.Org), Project: strings.ToLower(p.Project)}
	c, ok := clients[key]
	if !ok {
		hc, err := authshared.HTTPClient(ctx, f, p.Host, p.Org)
		if err != nil {
			return "", err
		}
		c = api.NewWorkItemClient(hc, authshared.OrgURL(p.Host, p.Org), p.Project)
		clients[key] = c
	}
	title, err := c.RestoreWorkItem(ctx, it.id)
	var he *api.HTTPError
	if errors.As(err, &he) && he.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("work item %d is not in the recycle bin", it.id)
	}
	return title, err
}
