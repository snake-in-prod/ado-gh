// Package clone implements `ado repo clone`.
package clone

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options are the inputs of Clone.
type Options struct {
	// Repo is the repository to clone.
	Repo adocontext.Repo
	// URL is a git URL the user typed, cloned as given; "" uses the repository's URL for the
	// host's git_protocol.
	URL string
	// Directory is the target directory; "" is the repository name.
	Directory string
	// GitFlags are passed to `git clone` before the URL.
	GitFlags []string
	// UpstreamName names the remote added for a fork's parent; "" is "upstream".
	UpstreamName string
}

// NewCmdClone returns the `repo clone` command.
func NewCmdClone(f *cmdutil.Factory) *cobra.Command {
	o := Options{}
	cmd := &cobra.Command{
		Use:   "clone <repo> [<directory>] [-- <gitflags>...]",
		Short: "Clone a repository locally",
		Long: `Clone an Azure DevOps repository. <repo> is REPO, PROJECT/REPO, ORG/PROJECT/REPO or a URL;
missing parts come from the current repository, ADO_ORG / ADO_PROJECT and the org config key.

The clone uses the repository's HTTPS or SSH URL according to the git_protocol config key of
its host. A git URL is cloned as given. HTTPS authentication is left to your git credential
setup (e.g. Git Credential Manager); ado never puts a token in a clone URL.

<directory> is a new directory name, without path separators, in the current directory; the
default is the repository name. A URL that contains a password is refused.

Cloning a fork adds its parent repository as the "upstream" remote and fetches it.

Pass additional git clone flags after "--".`,
		Example: `  ado repo clone api
  ado repo clone myproject/api api-copy
  ado repo clone myorg/myproject/api -- --depth 1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pos := args
			if n := cmd.ArgsLenAtDash(); n >= 0 {
				pos, o.GitFlags = args[:n], args[n:]
			}
			if len(pos) < 1 || len(pos) > 2 {
				return cmdutil.FlagErrorf("expected <repo> and an optional <directory>, then -- <gitflags>")
			}
			if len(pos) == 2 {
				o.Directory = pos[1]
				if err := validDir(o.Directory); err != nil {
					return cmdutil.FlagErrorWrap(err)
				}
			}
			if strings.HasPrefix(o.UpstreamName, "-") || o.UpstreamName == "" {
				return cmdutil.FlagErrorf("invalid upstream remote name %q", o.UpstreamName)
			}
			if hasPassword(pos[0]) {
				return errors.New("refusing to clone a URL that contains a password; store the credential in a git credential manager (e.g. Git Credential Manager) and pass the URL without it")
			}
			repo, err := shared.ParseRepo(f, pos[0])
			if err != nil {
				return err
			}
			o.Repo = repo
			if _, err := adocontext.ParseRemoteURL(pos[0]); err == nil {
				o.URL = pos[0]
			}
			return Clone(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVarP(&o.UpstreamName, "upstream-remote-name", "u", "upstream", "Name of the remote for the parent of a fork")
	return cmd
}

// hasPassword reports whether arg is a URL with a password in its userinfo.
func hasPassword(arg string) bool {
	u, err := url.Parse(arg)
	if err != nil || u.User == nil {
		return false
	}
	_, ok := u.User.Password()
	return ok
}

// validDir rejects directories that aren't a single new path element.
func validDir(dir string) error {
	if dir == "." || dir == ".." || filepath.Base(dir) != dir || strings.ContainsAny(dir, `/\`) {
		return fmt.Errorf("invalid directory %q: give a directory name without path separators", dir)
	}
	return nil
}

// Clone looks up o.Repo, clones it into o.Directory (default: the repository name) and, for a
// fork, adds and fetches its parent as o.UpstreamName. Git runs with the Factory's streams; a
// git failure returns cmdutil.SilentError, since git already reported it and its arguments
// hold the URL.
func Clone(ctx context.Context, f *cmdutil.Factory, o Options) error {
	if o.UpstreamName == "" {
		o.UpstreamName = "upstream"
	}
	client, err := shared.NewClient(ctx, f, o.Repo)
	if err != nil {
		return err
	}
	repo, err := client.Repository(ctx, o.Repo.Name)
	if err != nil {
		return err
	}
	ssh := false
	if cfg, err := f.Config(); err == nil {
		ssh = cfg.Get(o.Repo.Host, "git_protocol") == "ssh"
	}
	cloneURL := o.URL
	if cloneURL == "" {
		cloneURL = pick(ssh, repo.RemoteURL, repo.SSHURL)
		if cloneURL == "" {
			return fmt.Errorf("repository %s has no clone URL", repo.Name)
		}
	}
	dir := o.Directory
	if dir == "" {
		dir = cmp.Or(repo.Name, o.Repo.Name)
		if slices.Contains(o.GitFlags, "--bare") || slices.Contains(o.GitFlags, "--mirror") {
			dir += ".git"
		}
		if err := validDir(dir); err != nil {
			return err
		}
	}

	g := *f.GitClient
	ios := f.IOStreams
	streams := git.Streams{In: ios.In, Out: ios.Out, ErrOut: ios.ErrOut}
	args := append(append([]string{"clone"}, o.GitFlags...), "--", cloneURL, dir)
	if err := gitErr(g.ExecStream(ctx, streams, args...)); err != nil {
		return err
	}
	if !repo.IsFork || repo.ParentRepository == nil {
		return nil
	}

	p := repo.ParentRepository
	parentURL := pick(ssh, p.RemoteURL, p.SSHURL)
	if parentURL == "" && p.ID != "" {
		gr, err := (&api.PRClient{Git: client.Git}).RepositoryByID(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("cannot read the parent of fork %s: %w", repo.Name, err)
		}
		parentURL = pick(ssh, deref(gr.RemoteUrl), deref(gr.SshUrl))
	}
	if parentURL == "" {
		return fmt.Errorf("cannot find the clone URL of the parent of fork %s", repo.Name)
	}
	g.Dir = dir
	if err := gitErr(g.ExecStream(ctx, streams, "remote", "add", "--", o.UpstreamName, parentURL)); err != nil {
		return err
	}
	return gitErr(g.ExecStream(ctx, streams, "fetch", o.UpstreamName))
}

// gitErr turns a git failure into SilentError: git printed it, and its arguments hold URLs.
func gitErr(err error) error {
	var ge *git.Error
	if errors.As(err, &ge) {
		return cmdutil.SilentError
	}
	return err
}

func pick(ssh bool, https, sshURL string) string {
	if ssh {
		return sshURL
	}
	return https
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
