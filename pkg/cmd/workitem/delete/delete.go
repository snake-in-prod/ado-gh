// Package delete implements `ado workitem delete`.
package delete

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	refs           []string
	permanent, yes bool
}

// NewCmdDelete returns the `workitem delete` command.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "delete <id>...",
		Short: "Delete work items",
		Long: `Delete work items. Each <id> is an ID, #ID or a work item URL.

The work items move to the project's recycle bin, from which 'ado workitem restore'
brings them back. --permanent destroys them instead, which cannot be undone.

On a terminal, ado lists the work items and asks for confirmation; --yes skips that.
Deleting needs the project permission "Delete and restore work items"; --permanent
needs "Permanently delete work items".`,
		Example: `  ado workitem delete 123
  ado wi delete 123 124 --permanent --yes`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.refs = args
			if !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--yes required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVar(&o.permanent, "permanent", false, "Destroy the work items instead of moving them to the recycle bin (irreversible)")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

type item struct {
	id     int
	client *api.WorkItemClient
	// hint is the ref for the restore hint: the ID, or the work item's web URL for a URL ref.
	hint string
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	items, err := resolve(ctx, f, o.refs)
	if err != nil {
		return err
	}
	if !o.yes {
		if err := confirm(ctx, f, o.permanent, items); err != nil {
			return err
		}
	}

	ios := f.IOStreams
	cs := ios.ColorScheme()
	var done, failed, hints []string
	for _, it := range items {
		err := it.client.DeleteWorkItem(ctx, it.id, o.permanent)
		if err == nil {
			done, hints = append(done, strconv.Itoa(it.id)), append(hints, it.hint)
			if o.permanent {
				fmt.Fprintf(ios.ErrOut, "%s Permanently deleted work item %d\n", cs.Green("✓"), it.id)
			} else {
				fmt.Fprintf(ios.ErrOut, "%s Moved work item %d to the recycle bin\n", cs.Green("✓"), it.id)
			}
			continue
		}
		err = forbiddenHint(err, o.permanent)
		if len(items) == 1 {
			return err
		}
		failed = append(failed, strconv.Itoa(it.id))
		fmt.Fprintf(ios.ErrOut, "%s Failed to delete work item %d: %v\n", cs.Red("X"), it.id, err)
	}
	if !o.permanent && len(hints) > 0 {
		fmt.Fprintf(ios.ErrOut, "Restore with: ado wi restore %s\n", strings.Join(hints, " "))
	}
	if len(failed) == 0 {
		return nil
	}
	if len(done) == 0 {
		return fmt.Errorf("failed to delete work items %s", strings.Join(failed, ", "))
	}
	return fmt.Errorf("failed to delete work items %s; done: %s", strings.Join(failed, ", "), strings.Join(done, ", "))
}

// resolve parses refs into items, one client per project, dropping repeated items.
func resolve(ctx context.Context, f *cmdutil.Factory, refs []string) ([]item, error) {
	clients := map[adocontext.Repo]*api.WorkItemClient{}
	seen := map[string]bool{}
	var items []item
	for _, ref := range refs {
		id, project, err := shared.ParseRef(ref)
		if err != nil {
			return nil, err
		}
		hint := strconv.Itoa(id)
		if project.Project == "" {
			if project, err = f.BaseProject(); err != nil {
				return nil, err
			}
		} else {
			// Never echo ref itself: a URL's userinfo may hold a PAT.
			hint = shellQuote(adocontext.WorkItemURL(project, id))
		}
		key := adocontext.Repo{Host: project.Host, Org: strings.ToLower(project.Org), Project: strings.ToLower(project.Project)}
		k := fmt.Sprint(key, id)
		if seen[k] {
			continue
		}
		seen[k] = true
		c, ok := clients[key]
		if !ok {
			hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
			if err != nil {
				return nil, err
			}
			c = api.NewWorkItemClient(hc, authshared.OrgURL(project.Host, project.Org), project.Project)
			clients[key] = c
		}
		items = append(items, item{id: id, client: c, hint: hint})
	}
	return items, nil
}

// confirm lists the items with their titles and asks before deleting them.
func confirm(ctx context.Context, f *cmdutil.Factory, permanent bool, items []item) error {
	titles := map[*api.WorkItemClient]map[int]string{}
	for _, it := range items {
		if _, ok := titles[it.client]; ok {
			continue
		}
		var ids []int
		for _, o := range items {
			if o.client == it.client {
				ids = append(ids, o.id)
			}
		}
		// Missing items are left out of the batch; the delete reports them.
		wis, err := it.client.WorkItems(ctx, ids, []string{"System.Title"}, false)
		if err != nil {
			return err
		}
		titles[it.client] = map[int]string{}
		for _, w := range wis {
			titles[it.client][w.ID], _ = w.Fields["System.Title"].(string)
		}
	}
	var b strings.Builder
	what := "work item " + strconv.Itoa(items[0].id)
	if len(items) > 1 {
		what = fmt.Sprintf("%d work items", len(items))
	}
	if permanent {
		fmt.Fprintf(&b, "Permanently delete %s? This cannot be undone.", what)
	} else {
		fmt.Fprintf(&b, "Move %s to the recycle bin?", what)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "\n  %d %s", it.id, titles[it.client][it.id])
	}
	ok, err := f.Prompter.Confirm(b.String(), false)
	if err != nil {
		return err
	}
	if !ok {
		return cmdutil.CancelError
	}
	return nil
}

// forbiddenHint adds the permission a 403 most likely lacks. The docs require "Permanently
// delete work items" for any delete from the command line, so a soft delete names both.
func forbiddenHint(err error, permanent bool) error {
	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusForbidden {
		return err
	}
	if permanent {
		return fmt.Errorf(`%w
you need the project permission "Permanently delete work items"`, err)
	}
	return fmt.Errorf(`%w
you need the project permission "Delete and restore work items" (some organizations also require "Permanently delete work items")`, err)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._/:%-]+$`)

// shellQuote single-quotes s for POSIX shells unless it only has [A-Za-z0-9._/:%-].
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
