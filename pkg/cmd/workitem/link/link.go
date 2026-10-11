// Package link implements `ado workitem link`.
package link

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref   string
	links shared.LinkFlags
	yes   bool
}

// NewCmdLink returns the `workitem link` command.
func NewCmdLink(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "link <id>",
		Short: "Link a work item to work items, pull requests, commits, branches or URLs",
		Long: `Link a work item. <id> is an ID, #ID or a work item URL.

A work item has only one parent: --parent replaces an existing parent after
confirmation on a terminal; --yes replaces it without asking. --link takes any
work item link type by name or reference name, e.g. Predecessor=12 or "Tested By=7".
--pr, --commit and --branch link to the current (or -R) repository.`,
		Example: `  ado workitem link 123 --parent 100 --pr 45
  ado wi link 123 --child 124 --child 125 --link "Tested By=7"
  ado wi link 123 --commit 1a2b3c4 --url https://example.com/spec`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			return run(cmd.Context(), f, o)
		},
	}
	o.links.Register(cmd.Flags())
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Replace an existing parent without asking")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	t, err := shared.NewLinkTarget(ctx, f, o.ref)
	if err != nil {
		return err
	}
	links, err := o.links.Parse(ctx, t.Parser)
	if err != nil {
		return err
	}
	c := t.Parser.Client
	wi, err := c.WorkItem(ctx, t.ID, true)
	if err != nil {
		return err
	}

	ios := f.IOStreams
	var add []shared.Link
	for _, l := range links {
		if slices.ContainsFunc(wi.Relations, l.Matches) {
			fmt.Fprintf(ios.ErrOut, "! work item %d already has the %s link\n", t.ID, l)
		} else {
			add = append(add, l)
		}
	}
	// replace is the index of the parent relation a new parent replaces; -1 for none.
	replace := -1
	if i := slices.IndexFunc(add, func(l shared.Link) bool { return l.Rel == shared.ParentRel }); i >= 0 {
		replace = slices.IndexFunc(wi.Relations, func(r api.WorkItemRelation) bool { return strings.EqualFold(r.Rel, shared.ParentRel) })
		if replace >= 0 && !o.yes {
			if err := confirmReplace(f, t.ID, wi.Relations[replace].URL, add[i].ID); err != nil {
				return err
			}
		}
	}

	var done, failed []string
	for _, l := range add {
		ops := []webapi.JsonPatchOperation{l.AddOp()}
		if l.Rel == shared.ParentRel && replace >= 0 {
			// One patch, so the work item never has two parents or none; the rev test keeps
			// the index valid.
			test, remove := webapi.OperationValues.Test, webapi.OperationValues.Remove
			revPath, relPath := "/rev", "/relations/"+strconv.Itoa(replace)
			ops = []webapi.JsonPatchOperation{{Op: &test, Path: &revPath, Value: wi.Rev}, {Op: &remove, Path: &relPath}, ops[0]}
			replace = -1
		}
		w, err := c.UpdateWorkItem(ctx, t.ID, ops)
		if w != nil {
			wi.Rev = w.Rev
		}
		if err = addErr(t.ID, l, err); err != nil {
			if len(add) == 1 {
				return err
			}
			fmt.Fprintf(ios.ErrOut, "X failed to add %s: %v\n", l, err)
			failed = append(failed, l.String())
			continue
		}
		done = append(done, l.String())
	}
	if len(failed) > 0 {
		if len(done) == 0 {
			done = []string{"none"}
		}
		fmt.Fprintf(ios.ErrOut, "added: %s; failed: %s\n", strings.Join(done, ", "), strings.Join(failed, ", "))
		return cmdutil.SilentError
	}
	fmt.Fprintln(ios.Out, adocontext.WorkItemURL(t.Project, t.ID))
	return nil
}

// confirmReplace asks before replacing the parent at oldURL of work item id with newID; it
// refuses when it cannot ask.
func confirmReplace(f *cmdutil.Factory, id int, oldURL string, newID int) error {
	old := oldURL
	if oldID, _, err := shared.ParseRef(oldURL); err == nil {
		old = "#" + strconv.Itoa(oldID)
	}
	if !f.IOStreams.CanPrompt() {
		return fmt.Errorf("work item %d already has parent %s; pass --yes to replace it", id, old)
	}
	ok, err := f.Prompter.Confirm(fmt.Sprintf("Replace parent %s of work item %d with #%d?", old, id, newID), false)
	if err != nil {
		return err
	}
	if !ok {
		return cmdutil.CancelError
	}
	return nil
}

// addErr explains a failed add of l to work item id, matching server errors by code:
// TF201035 (the relation exists) is success, TF201036 breaks the one-parent rule.
func addErr(id int, l shared.Link, err error) error {
	switch {
	case err == nil || strings.Contains(err.Error(), "TF201035"):
		return nil
	case api.IsRevConflict(err):
		return fmt.Errorf("work item %d changed since read; retry", id)
	case strings.Contains(err.Error(), "TF201036") && l.Rel == shared.ChildRel:
		return fmt.Errorf("%w\nwork item %d already has a parent; replace it with: ado wi link %d --parent %d", err, l.ID, l.ID, id)
	case strings.Contains(err.Error(), "TF201036") && l.Rel == shared.ParentRel:
		return fmt.Errorf("%w\na work item can have only one parent", err)
	}
	return err
}
