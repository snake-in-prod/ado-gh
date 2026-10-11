// Package list implements `ado pr list`.
package list

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

var states = []string{"active", "completed", "abandoned", "all"}

type options struct {
	state, author, base, head, reviewer string
	limit                               int
	draft, web                          bool
	exporter                            *cmdutil.Exporter
}

// NewCmdList returns the `pr list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List pull requests in a repository",
		Example: `  ado pr list --author @me
  ado pr list --state all --base main --limit 100
  ado pr list --json id,title --jq '.[].id'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains(states, o.state) {
				return cmdutil.FlagErrorf("invalid value for --state: %q (use %s)", o.state, strings.Join(states, ", "))
			}
			if o.limit < 1 {
				return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.state, "state", "s", "active", "Filter by state: {active|completed|abandoned|all}")
	fl.StringVarP(&o.author, "author", "A", "", "Filter by author (email, name, GUID or @me)")
	fl.StringVarP(&o.base, "base", "B", "", "Filter by target branch")
	fl.StringVarP(&o.head, "head", "H", "", "Filter by source branch")
	fl.StringVar(&o.reviewer, "reviewer", "", "Filter by reviewer (email, name, [Project]\\Team, GUID or @me)")
	fl.IntVarP(&o.limit, "limit", "L", 30, "Maximum number of pull requests to fetch")
	fl.BoolVar(&o.draft, "draft", false, "Show only draft pull requests")
	fl.BoolVarP(&o.web, "web", "w", false, "List pull requests in the web browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, shared.JSONFields)
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	repo, err := f.BaseRepo()
	if err != nil {
		return err
	}
	ios := f.IOStreams
	orgURL := authshared.OrgURL(repo.Host, repo.Org)
	repoURL := shared.RepoURL(repo)

	if o.web {
		u := repoURL + "/pullrequests"
		if o.state != "all" {
			u += "?_a=" + o.state
		}
		return cmdutil.OpenInBrowser(f, u)
	}

	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return err
	}
	status := git.PullRequestStatus(o.state)
	criteria := git.GitPullRequestSearchCriteria{Status: &status, SourceRefName: refName(o.head), TargetRefName: refName(o.base)}
	resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
	if criteria.CreatorId, err = resolveOne(ctx, resolver, o.author); err != nil {
		return err
	}
	if criteria.ReviewerId, err = resolveOne(ctx, resolver, o.reviewer); err != nil {
		return err
	}

	prs, err := api.NewPRClient(hc, orgURL, repo.Project, repo.Name).ListPullRequests(ctx, criteria, o.limit, o.draft)
	if err != nil {
		return err
	}

	if o.exporter != nil {
		out := make([]shared.PRJSON, len(prs))
		for i := range prs {
			out[i] = shared.ToJSON(prs[i], repoURL)
		}
		return o.exporter.Write(ios, out)
	}

	what := "pull requests"
	if o.state != "all" {
		what = o.state + " " + what
	}
	if len(prs) == 0 {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "No %s match your search in %s/%s\n", what, repo.Project, repo.Name)
		}
		return nil
	}
	return printTable(f, repo, what, prs)
}

func printTable(f *cmdutil.Factory, repo adocontext.Repo, what string, prs []git.GitPullRequest) error {
	ios := f.IOStreams
	tty := ios.IsStdoutTTY()
	if tty {
		fmt.Fprintf(ios.Out, "\nShowing %d %s in %s/%s\n\n", len(prs), what, repo.Project, repo.Name)
	}
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "id", "title", "branch", "created")
	now := time.Now()
	for _, pr := range prs {
		state := shared.Deref(pr.Status)
		if pr.IsDraft != nil && *pr.IsDraft {
			state = "draft"
		}
		id := strconv.Itoa(shared.Deref(pr.PullRequestId))
		if tty {
			id = "!" + id
		}
		tp.AddField(id, tableprinter.WithColor(map[git.PullRequestStatus]func(string) string{
			"active": cs.Green, "draft": cs.Gray, "completed": cs.Magenta, "abandoned": cs.Red,
		}[state]))
		tp.AddField(shared.Deref(pr.Title))
		tp.AddField(strings.TrimPrefix(shared.Deref(pr.SourceRefName), "refs/heads/"), tableprinter.WithColor(cs.Cyan))
		if !tty {
			tp.AddField(string(state))
		}
		var created time.Time
		if pr.CreationDate != nil {
			created = pr.CreationDate.Time
		}
		tp.AddTimeField(now, created, cs.Gray)
		tp.EndRow()
	}
	return tp.Render()
}

// refName returns branch as a full ref (refs/heads/ added unless it starts with refs/), or nil.
func refName(branch string) *string {
	if branch == "" {
		return nil
	}
	if !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}
	return &branch
}

func resolveOne(ctx context.Context, r *api.IdentityResolver, v string) (*uuid.UUID, error) {
	ids, err := r.Resolve(ctx, []string{v})
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return &ids[0], nil
}
