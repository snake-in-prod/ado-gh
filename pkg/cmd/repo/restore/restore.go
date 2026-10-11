// Package restore implements `ado repo restore`.
package restore

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRestore returns the `repo restore` command.
func NewCmdRestore(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <repo>",
		Short: "Restore a deleted repository from the recycle bin",
		Long: `Restore a soft-deleted repository from its project's recycle bin. Deleted
repositories stay in the recycle bin for 30 days.

<repo> is a repository name or ID, optionally prefixed with PROJECT/ or ORG/PROJECT/;
the project defaults to the current one.`,
		Example: `  ado repo restore api
  ado repo restore myproject/api
  ado repo restore myorg/myproject/11111111-2222-3333-4444-555555555555`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := shared.ParseRepo(f, args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			client, err := shared.NewClient(ctx, f, repo)
			if err != nil {
				return err
			}
			deleted, err := client.RecycleBinRepositories(ctx)
			if err != nil {
				return err
			}
			d, err := find(deleted, repo)
			if err != nil {
				return err
			}
			r, err := client.RestoreRepository(ctx, d.ID)
			if err != nil {
				return err
			}
			repo.Name = cmp.Or(r.Name, d.Name)
			ios := f.IOStreams
			if ios.IsStdoutTTY() {
				fmt.Fprintf(ios.ErrOut, "Restored repository %s/%s\n", repo.Project, repo.Name)
			}
			fmt.Fprintln(ios.Out, cmp.Or(r.WebURL, adocontext.RepoURL(repo)))
			return nil
		},
	}
}

// find returns the deleted repository whose ID or name matches repo.Name (case-insensitive).
func find(deleted []api.DeletedRepository, repo adocontext.Repo) (api.DeletedRepository, error) {
	var matches []api.DeletedRepository
	for _, d := range deleted {
		if strings.EqualFold(d.ID, repo.Name) {
			return d, nil
		}
		if strings.EqualFold(d.Name, repo.Name) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return api.DeletedRepository{}, fmt.Errorf("no repository %q in the recycle bin of project %s", repo.Name, repo.Project)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	return api.DeletedRepository{}, fmt.Errorf("several deleted repositories are named %q; restore one by ID: %s",
		repo.Name, strings.Join(ids, ", "))
}
