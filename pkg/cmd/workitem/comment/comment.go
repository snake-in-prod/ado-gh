// Package comment implements `ado workitem comment`.
package comment

import (
	"context"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref                       string
	body                      cmdutil.BodyFlags
	editLast, deleteLast, yes bool
}

// NewCmdComment returns the `workitem comment` command.
func NewCmdComment(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "comment <id> | <url>",
		Short: "Add a comment to a work item",
		Long: `Add a comment to a work item, or edit or delete your most recent one.

Without a body flag, the text editor opens on a terminal. The comment is sent as Markdown.

--edit-last replaces the text of your most recent comment; without a body flag the editor
opens on its current text. --delete-last deletes it after a confirmation, unless --yes is
given.

The URL of the comment is printed.`,
		Example: `  ado workitem comment 123 --body "Fixed in !45"
  ado wi comment 123 -F notes.md
  ado wi comment 123 --edit-last -b "Fixed in !46"
  ado wi comment 123 --delete-last --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			if o.yes && !o.deleteLast {
				return cmdutil.FlagErrorf("--yes should only be used with --delete-last")
			}
			if o.deleteLast && (o.body.Given() || o.body.Editor) {
				return cmdutil.FlagErrorf("should not provide comment body when using --delete-last")
			}
			if o.deleteLast && !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("should provide --yes to confirm deletion in non-interactive mode")
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmdutil.AddBodyFlags(cmd, &o.body)
	fl := cmd.Flags()
	fl.BoolVar(&o.editLast, "edit-last", false, "Edit your most recent comment")
	fl.BoolVar(&o.deleteLast, "delete-last", false, "Delete your most recent comment")
	fl.BoolVarP(&o.yes, "yes", "y", false, "Skip the delete confirmation prompt (with --delete-last)")
	cmd.MarkFlagsMutuallyExclusive("edit-last", "delete-last")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	id, project, err := shared.ParseRef(o.ref)
	if err != nil {
		return err
	}
	if project.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return err
		}
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(project.Host, project.Org)
	c := api.NewWorkItemClient(hc, orgURL, project.Project)
	ios := f.IOStreams

	if !o.editLast && !o.deleteLast {
		body, err := o.body.Read(f, "")
		if err != nil {
			return err
		}
		cm, err := c.AddComment(ctx, id, body, true)
		if err != nil {
			return err
		}
		fmt.Fprintln(ios.Out, commentURL(project, id, cm.ID))
		return nil
	}

	last, err := lastComment(ctx, c, &api.IdentityResolver{HTTP: hc, OrgURL: orgURL}, id)
	if err != nil {
		return err
	}
	if o.deleteLast {
		if !o.yes {
			ok, err := f.Prompter.Confirm(fmt.Sprintf("Delete the comment: %q?", preview(last.Text)), true)
			if err != nil {
				return err
			}
			if !ok {
				return cmdutil.CancelError
			}
		}
		if err := c.DeleteComment(ctx, id, last.ID); err != nil {
			return err
		}
		fmt.Fprintln(ios.ErrOut, "Comment deleted")
		return nil
	}

	current := last.Text
	if !strings.EqualFold(last.Format, "markdown") {
		current = shared.HTMLToText(current) // the edited text is sent as Markdown
	}
	body, err := o.body.Read(f, current)
	if err != nil {
		return err
	}
	if _, err := c.UpdateComment(ctx, id, last.ID, body, true); err != nil {
		return err
	}
	fmt.Fprintln(ios.Out, commentURL(project, id, last.ID))
	return nil
}

// lastComment returns the signed-in user's most recent comment on work item id that isn't
// deleted, paging newest first until one is found.
func lastComment(ctx context.Context, c *api.WorkItemClient, r *api.IdentityResolver, id int) (*api.WorkItemComment, error) {
	ids, err := r.Resolve(ctx, []string{"@me"})
	if err != nil {
		return nil, err
	}
	me := ids[0].String()
	token := ""
	for {
		page, next, err := c.Comments(ctx, id, true, token)
		if err != nil {
			return nil, err
		}
		for _, cm := range page {
			if !cm.IsDeleted && strings.EqualFold(cm.CreatedBy.ID, me) {
				return &cm, nil
			}
		}
		if next == "" {
			return nil, fmt.Errorf("no comment of yours found on work item %d", id)
		}
		token = next
	}
}

// commentURL is the web link to comment commentID of work item id.
func commentURL(project adocontext.Repo, id, commentID int) string {
	return fmt.Sprintf("%s#%d", adocontext.WorkItemURL(project, id), commentID)
}

// preview shortens s to one line of at most 40 characters for the delete prompt.
func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}
