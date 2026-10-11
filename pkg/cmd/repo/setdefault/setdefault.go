// Package setdefault implements `ado repo set-default`.
package setdefault

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	arg         string
	view, unset bool
}

// NewCmdSetDefault returns the `repo set-default` command.
func NewCmdSetDefault(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "set-default [<repo>]",
		Short: "Configure the default repository for this directory",
		Long: `Set the repository that ado commands target in this git checkout when -R and ADO_REPO
are not given. <repo> is a git remote name, or REPO, PROJECT/REPO, ORG/PROJECT/REPO or a URL
matching one of the Azure DevOps remotes; a remote name wins over a repository name.

Without <repo>, ado picks the only Azure DevOps repository among the remotes, or asks on a
terminal. The choice is stored as remote.<name>.ado-resolved in the git config.`,
		Example: `  ado repo set-default upstream
  ado repo set-default myproject/api
  ado repo set-default --view
  ado repo set-default --unset`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.arg = strings.Join(args, "")
			switch {
			case f.RepoOverride != "":
				return cmdutil.FlagErrorf("-R/--repo is not supported; pass the repository as an argument")
			case o.arg != "" && (o.view || o.unset):
				return cmdutil.FlagErrorf("cannot combine <repo> with --view or --unset")
			case o.arg == "" && !o.view && !o.unset && !f.IOStreams.CanPrompt():
				return cmdutil.FlagErrorf("repository required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVarP(&o.view, "view", "v", false, "Print the current default repository")
	cmd.Flags().BoolVarP(&o.unset, "unset", "u", false, "Remove the default repository")
	cmd.MarkFlagsMutuallyExclusive("view", "unset")
	return cmd
}

// adoRemote is a git remote that points at an Azure DevOps repository.
type adoRemote struct {
	git.Remote
	repo adocontext.Repo
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	remotes, err := f.GitClient.ResolvedRemotes(ctx)
	if errors.Is(err, git.ErrNotGitRepo) {
		return errors.New("must be run from inside a git repository")
	}
	if err != nil {
		return err
	}
	var ado []adoRemote
	for _, r := range remotes {
		if repo, err := adocontext.ParseRemoteURL(r.FetchURL); err == nil {
			ado = append(ado, adoRemote{r, repo})
		}
	}
	// As adocontext.ResolveBaseRepo: the first ADO remote marked "base" is the default.
	current := slices.IndexFunc(ado, func(r adoRemote) bool { return r.Resolved == "base" })

	ios := f.IOStreams
	check := ios.ColorScheme().Green("✓")
	switch {
	case o.view:
		if current < 0 {
			fmt.Fprintln(ios.ErrOut, "no default repository has been set; use `ado repo set-default` to select one")
			return nil
		}
		fmt.Fprintln(ios.Out, fullName(ado[current].repo))
		return nil
	case o.unset:
		if err := unsetAll(ctx, f, remotes); err != nil {
			return err
		}
		if current < 0 {
			fmt.Fprintln(ios.ErrOut, "no default repository has been set")
		} else {
			fmt.Fprintf(ios.ErrOut, "%s Unset %s as the default repository for the current directory\n", check, fullName(ado[current].repo))
		}
		return nil
	}

	target, err := pick(f, o.arg, remotes, ado)
	if err != nil {
		return err
	}
	if err := unsetAll(ctx, f, remotes); err != nil {
		return err
	}
	// A plain set, not --add: duplicate values would make a later --unset fail.
	if _, err := f.GitClient.Exec(ctx, "config", "remote."+target.Name+".ado-resolved", "base"); err != nil {
		return err
	}
	fmt.Fprintf(ios.ErrOut, "%s Set %s as the default repository for the current directory\n", check, fullName(target.repo))
	return nil
}

// pick returns the ADO remote named or matched by arg, or chosen among the distinct ADO
// repositories when arg is "".
func pick(f *cmdutil.Factory, arg string, remotes []git.Remote, ado []adoRemote) (adoRemote, error) {
	if arg != "" {
		name := arg
		if !slices.ContainsFunc(remotes, func(r git.Remote) bool { return r.Name == arg }) {
			repo, err := shared.ParseRepo(f, arg)
			if err != nil {
				return adoRemote{}, err
			}
			if name = adocontext.MatchRemote(remotes, repo); name == "" {
				return adoRemote{}, fmt.Errorf("%s does not match any Azure DevOps remote of this repository", fullName(repo))
			}
		}
		i := slices.IndexFunc(ado, func(r adoRemote) bool { return r.Name == name })
		if i < 0 {
			return adoRemote{}, fmt.Errorf("remote %q is not an Azure DevOps remote", name)
		}
		return ado[i], nil
	}

	var repos []adoRemote // one per repository, in remote order
	for _, r := range ado {
		if !slices.ContainsFunc(repos, func(d adoRemote) bool { return adocontext.PointsAt(r.FetchURL, d.repo) }) {
			repos = append(repos, r)
		}
	}
	switch len(repos) {
	case 0:
		return adoRemote{}, errors.New("none of the git remotes point to an Azure DevOps repository")
	case 1:
		fmt.Fprintf(f.IOStreams.ErrOut, "Found only one Azure DevOps repository: %s\n", fullName(repos[0].repo))
		return repos[0], nil
	}
	labels := make([]string, len(repos))
	for i, r := range repos {
		labels[i] = fullName(r.repo)
	}
	i, err := f.Prompter.Select("Which repository should be the default?", labels)
	if err != nil {
		return adoRemote{}, err
	}
	return repos[i], nil
}

// unsetAll removes the ado-resolved key from every remote that has one.
func unsetAll(ctx context.Context, f *cmdutil.Factory, remotes []git.Remote) error {
	for _, r := range remotes {
		if r.Resolved != "" {
			if _, err := f.GitClient.Exec(ctx, "config", "--unset-all", "remote."+r.Name+".ado-resolved"); err != nil {
				return err
			}
		}
	}
	return nil
}

func fullName(r adocontext.Repo) string {
	return r.Org + "/" + r.Project + "/" + r.Name
}
