// Package create implements `ado pr create`.
package create

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	title, body, bodyFile, base, head string
	reviewers                         []string
	workItems                         []int
	draft, fill, web, auto            bool
	titleSet, bodySet                 bool
}

// NewCmdCreate returns the `pr create` command.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a pull request",
		Long: `Create a pull request from the current branch (or --head) into the repository's
default branch (or --base).

The branch is pushed first if it is not on the remote (after a prompt on a terminal).
On a terminal, a missing title, description and reviewers are prompted for; otherwise
--title (or --fill) is required. Reviewers are added as optional reviewers.`,
		Example: `  ado pr create --fill
  ado pr create -t "Add login" -b "Closes the login story" -r alice@contoso.com --work-item 42
  ado pr create --draft --base develop --auto`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.titleSet = cmd.Flags().Changed("title")
			o.bodySet = cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
			if !o.titleSet && !o.fill && !o.web && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--title (or --fill) required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.title, "title", "t", "", "Title for the pull request")
	fl.StringVarP(&o.body, "body", "b", "", "Description for the pull request")
	fl.StringVarP(&o.bodyFile, "body-file", "F", "", `Read the description from file (use "-" for stdin)`)
	fl.StringVarP(&o.base, "base", "B", "", "Target branch (default: the repository's default branch)")
	fl.StringVarP(&o.head, "head", "H", "", "Source branch (default: the current branch)")
	fl.BoolVarP(&o.draft, "draft", "d", false, "Create a draft pull request")
	fl.StringSliceVarP(&o.reviewers, "reviewer", "r", nil, "Add optional reviewers (email, name, [Project]\\Team, GUID or @me)")
	fl.IntSliceVar(&o.workItems, "work-item", nil, "Link work items by ID")
	fl.BoolVarP(&o.fill, "fill", "f", false, "Use commit info for the title and description")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the web browser to create a pull request")
	fl.BoolVar(&o.auto, "auto", false, "Set auto-complete after creating the pull request")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.MarkFlagsMutuallyExclusive("web", "auto")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	ios := f.IOStreams
	repo, err := f.BaseRepo()
	if err != nil {
		return err
	}
	if o.bodyFile != "" {
		if o.body, err = readBody(f, o.bodyFile); err != nil {
			return err
		}
	}
	head, pushCheck := strings.TrimPrefix(o.head, "refs/heads/"), o.head == ""
	if pushCheck {
		if head, err = f.GitClient.CurrentBranch(ctx); errors.Is(err, gitcmd.ErrNotOnAnyBranch) {
			return errors.New("not on any branch; pass the source branch with --head")
		} else if err != nil {
			return err
		}
	}
	remote := findRemote(ctx, f.GitClient, repo)
	base := strings.TrimPrefix(o.base, "refs/heads/")

	if o.web {
		if pushCheck {
			if err := ensurePushed(ctx, f, remote, head); err != nil {
				return err
			}
		}
		q := url.Values{"sourceRef": {head}}
		if base != "" {
			q.Set("targetRef", base)
		}
		u := shared.RepoURL(repo) + "/pullrequestcreate?" + q.Encode()
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "Opening %s in your browser.\n", u)
		}
		return f.Browser.Browse(u)
	}

	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(repo.Host, repo.Org)
	c := api.NewPRClient(hc, orgURL, repo.Project, repo.Name)
	if base == "" {
		r, err := c.Repository(ctx)
		if err != nil {
			return err
		}
		if r.DefaultBranch == nil {
			return errors.New("the repository has no default branch; pass the target branch with --base")
		}
		base = strings.TrimPrefix(*r.DefaultBranch, "refs/heads/")
	}

	if o.fill {
		if err := fill(ctx, f.GitClient, o, remote, base, head); err != nil {
			return err
		}
	} else if !o.titleSet {
		if err := prompt(f.Prompter, o); err != nil {
			return err
		}
	}

	resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
	ids, err := resolver.Resolve(ctx, o.reviewers)
	if err != nil {
		return err
	}
	if pushCheck {
		if err := ensurePushed(ctx, f, remote, head); err != nil {
			return err
		}
	}

	src, dst := "refs/heads/"+head, "refs/heads/"+base
	pr := &git.GitPullRequest{Title: &o.title, Description: &o.body, SourceRefName: &src, TargetRefName: &dst, IsDraft: &o.draft}
	if len(ids) > 0 {
		reviewers := make([]git.IdentityRefWithVote, len(ids))
		for i, id := range ids {
			s := id.String()
			reviewers[i].Id = &s
		}
		pr.Reviewers = &reviewers
	}
	if len(o.workItems) > 0 {
		refs := make([]webapi.ResourceRef, len(o.workItems))
		for i, w := range o.workItems {
			s := strconv.Itoa(w)
			refs[i].Id = &s
		}
		pr.WorkItemRefs = &refs
	}

	if ios.IsStdoutTTY() {
		what := "pull request"
		if o.draft {
			what = "draft " + what
		}
		fmt.Fprintf(ios.ErrOut, "Creating %s for %s into %s in %s/%s\n\n", what, head, base, repo.Project, repo.Name)
	}
	created, err := c.CreatePullRequest(ctx, pr)
	if err != nil {
		return err
	}
	prURL := shared.PRURL(shared.RepoURL(repo), *created)

	if o.auto {
		// Auto-complete can't be set on create; it takes a second update.
		me, err := resolver.Resolve(ctx, []string{"@me"})
		if err != nil {
			return fmt.Errorf("created %s but could not set auto-complete: %w", prURL, err)
		}
		meID := me[0].String()
		// Empty completion options leave the merge strategy to the repository's defaults and policies.
		upd := &git.GitPullRequest{AutoCompleteSetBy: &webapi.IdentityRef{Id: &meID}, CompletionOptions: &git.GitPullRequestCompletionOptions{}}
		if _, err := c.UpdatePullRequest(ctx, *created.PullRequestId, upd); err != nil {
			return fmt.Errorf("created %s but could not set auto-complete: %w", prURL, err)
		}
	}
	fmt.Fprintln(ios.Out, prURL)
	return nil
}

func readBody(f *cmdutil.Factory, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(f.IOStreams.In)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("reading the description: %w", err)
	}
	return string(b), nil
}

// findRemote returns the name of the git remote pointing at repo, else "origin".
func findRemote(ctx context.Context, gc *gitcmd.Client, repo adocontext.Repo) string {
	remotes, _ := gc.Remotes(ctx)
	for _, r := range remotes {
		got, err := adocontext.ParseRemoteURL(r.FetchURL)
		if err == nil && strings.EqualFold(got.Org, repo.Org) && strings.EqualFold(got.Project, repo.Project) && strings.EqualFold(got.Name, repo.Name) {
			return r.Name
		}
	}
	return "origin"
}

// ensurePushed pushes head to remote when there is no remote-tracking branch for it, after a
// confirmation on a terminal.
// ponytail: checks only that the tracking ref exists, not that it is up to date; compare with
// the local branch if unpushed commits on an existing branch turn out to matter.
func ensurePushed(ctx context.Context, f *cmdutil.Factory, remote, head string) error {
	if _, err := f.GitClient.Exec(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+head); err == nil {
		return nil
	}
	ios := f.IOStreams
	if ios.CanPrompt() {
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Push branch %s to %s?", head, remote), true)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}
	fmt.Fprintf(ios.ErrOut, "Pushing %s to %s\n", head, remote)
	_, err := f.GitClient.Exec(ctx, "push", "--set-upstream", remote, "HEAD:refs/heads/"+head)
	return err
}

// fill sets the title and description not given as flags from the commits on head that are
// not on the remote base: one commit gives its subject and body, several give the branch name
// and a list of subjects, oldest first.
func fill(ctx context.Context, gc *gitcmd.Client, o *options, remote, base, head string) error {
	out, err := gc.Exec(ctx, "-c", "log.showSignature=false", "log", "--format=%x1e%s%x1f%b", "refs/remotes/"+remote+"/"+base+".."+head)
	if err != nil {
		return err
	}
	var commits [][2]string
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		if subject, body, ok := strings.Cut(rec, "\x1f"); ok {
			commits = append(commits, [2]string{subject, strings.TrimSpace(body)})
		}
	}
	title, body := head, ""
	if len(commits) == 1 {
		title, body = commits[0][0], commits[0][1]
	} else if len(commits) > 1 {
		lines := make([]string, 0, len(commits))
		for i := len(commits) - 1; i >= 0; i-- {
			lines = append(lines, "- "+commits[i][0])
		}
		body = strings.Join(lines, "\n")
	}
	if !o.titleSet {
		o.title = title
	}
	if !o.bodySet {
		o.body = body
	}
	return nil
}

// prompt asks for the title, and for the description and reviewers not given as flags.
func prompt(p *cmdutil.Prompter, o *options) error {
	var err error
	if o.title, err = p.Input("Title", ""); err != nil {
		return err
	}
	if strings.TrimSpace(o.title) == "" {
		return errors.New("a title is required")
	}
	if !o.bodySet {
		if o.body, err = p.Input("Description", ""); err != nil {
			return err
		}
	}
	if len(o.reviewers) == 0 {
		r, err := p.Input("Reviewers (comma-separated, optional)", "")
		if err != nil {
			return err
		}
		o.reviewers = strings.Split(r, ",")
	}
	return nil
}
