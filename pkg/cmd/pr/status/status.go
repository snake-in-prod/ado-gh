// Package status implements `ado pr status`.
package status

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

const (
	// sectionLimit caps the pull requests shown per section, as gh does.
	sectionLimit = 10
	// reviewCandidates caps the active pull requests scanned for review requests.
	reviewCandidates = 200
)

var jsonFields = append(slices.Clone(shared.JSONFields), "checks", "reviewDecision")

type options struct {
	conflictStatus, project bool
	exporter                *cmdutil.Exporter
}

// NewCmdStatus returns the `pr status` command.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of relevant pull requests",
		Long: `Show the pull request of the current branch, your active pull requests and the active
pull requests waiting on your review, directly or through a group or team you belong to.

With --project, the last two sections cover every repository of the project.`,
		Example: `  ado pr status
  ado pr status --project -c
  ado pr status --json id,title,checks --jq '.needsReview[].id'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVarP(&o.conflictStatus, "conflict-status", "c", false, "Display the merge conflict status of each pull request")
	cmd.Flags().BoolVar(&o.project, "project", false, "Include pull requests of every repository in the project")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

// statusJSON is the `--json` shape of a pull request in a section.
type statusJSON struct {
	shared.PRJSON
	Checks         []shared.Check `json:"checks"`
	ReviewDecision string         `json:"reviewDecision"`
}

// row is a listed pull request with its repository and checks.
type row struct {
	pr      git.GitPullRequest
	repoURL string
	// repoName prefixes the number with --project.
	repoName string
	checks   []shared.Check
}

type statusRun struct {
	f        *cmdutil.Factory
	o        *options
	repo     adocontext.Repo
	c        *api.PRClient
	me       string
	resolver *api.IdentityResolver
	// withChecks fetches each row's checks.
	withChecks bool
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	repo, err := f.BaseRepo()
	if err != nil {
		return err
	}
	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(repo.Host, repo.Org)
	s := &statusRun{
		f: f, o: o, repo: repo, c: api.NewPRClient(hc, orgURL, repo.Project, repo.Name),
		resolver:   &api.IdentityResolver{HTTP: hc, OrgURL: orgURL},
		withChecks: o.exporter == nil || slices.Contains(o.exporter.Fields, "checks") || slices.Contains(o.exporter.Fields, "reviewDecision"),
	}
	ids, err := s.resolver.Resolve(ctx, []string{"@me"})
	if err != nil {
		return err
	}
	s.me = strings.ToLower(ids[0].String())

	// Current branch: repository-scoped even with --project, hidden with -R (as in gh) or
	// ADO_REPO, when the current directory may be another repository.
	showCurrent := f.BaseRepoFromGit()
	var branch string
	var current []row
	if showCurrent {
		if branch, err = f.GitClient.CurrentBranch(ctx); errors.Is(err, gitcmd.ErrNotOnAnyBranch) {
			branch = ""
		} else if err != nil {
			return err
		}
	}
	if branch != "" {
		pr, err := shared.FindForBranch(ctx, s.c, branch)
		if err != nil {
			return err
		}
		if pr != nil {
			if current, err = s.rows(ctx, []git.GitPullRequest{*pr}); err != nil {
				return err
			}
		}
	}

	scope := s.c
	if o.project {
		pc := *s.c
		pc.Repo = ""
		scope = &pc
	}
	active := git.PullRequestStatusValues.Active
	created, err := scope.ListPullRequests(ctx, git.GitPullRequestSearchCriteria{Status: &active, CreatorId: &ids[0]}, sectionLimit+1, false)
	if err != nil {
		return err
	}
	candidates, err := scope.ListPullRequests(ctx, git.GitPullRequestSearchCriteria{Status: &active}, reviewCandidates, false)
	if err != nil {
		return err
	}
	review, err := s.needsReview(ctx, candidates)
	if err != nil {
		return err
	}
	createdMore, reviewMore := len(created) > sectionLimit, len(review) > sectionLimit
	createdRows, err := s.rows(ctx, created[:min(len(created), sectionLimit)])
	if err != nil {
		return err
	}
	reviewRows, err := s.rows(ctx, review[:min(len(review), sectionLimit)])
	if err != nil {
		return err
	}

	if o.exporter != nil {
		data := map[string]any{"currentBranch": nil, "createdBy": toJSON(createdRows), "needsReview": toJSON(reviewRows)}
		if len(current) > 0 {
			data["currentBranch"] = toJSON(current)[0]
		}
		return o.exporter.WriteSections(f.IOStreams, data)
	}

	ios := f.IOStreams
	w, cs := ios.Out, ios.ColorScheme()
	where := repo.Project + "/" + repo.Name
	if o.project {
		where = repo.Project
	}
	fmt.Fprintf(w, "\nRelevant pull requests in %s\n\n", where)
	if showCurrent {
		fmt.Fprintln(w, cs.Bold("Current branch"))
		switch {
		case len(current) > 0:
			s.printRows(w, cs, current, false)
		case branch == "":
			fmt.Fprintln(w, cs.Gray("  There is no current branch"))
		default:
			fmt.Fprintln(w, cs.Gray("  There is no pull request associated with ")+cs.Cyan("["+branch+"]"))
		}
		fmt.Fprintln(w)
	}
	s.printSection(w, cs, "Created by you", createdRows, createdMore, "You have no open pull requests")
	s.printSection(w, cs, "Requesting a code review from you", reviewRows, reviewMore, "You have no pull requests to review")
	return nil
}

// rows wraps prs with their repository and, when needed, their checks.
func (s *statusRun) rows(ctx context.Context, prs []git.GitPullRequest) ([]row, error) {
	out := make([]row, len(prs))
	for i, pr := range prs {
		out[i] = row{pr: pr, repoURL: shared.PRRepoURL(s.repo, pr)}
		c := s.c
		if pr.Repository != nil && pr.Repository.Name != nil {
			rc := *s.c
			rc.Repo = *pr.Repository.Name
			c = &rc
			if s.o.project {
				out[i].repoName = rc.Repo
			}
		}
		if s.withChecks {
			checks, err := shared.Checks(ctx, c, &out[i].pr, out[i].repoURL)
			if err != nil {
				return nil, err
			}
			out[i].checks = checks
		}
	}
	return out, nil
}

func toJSON(rows []row) []statusJSON {
	out := make([]statusJSON, len(rows))
	for i, r := range rows {
		checks := r.checks
		if checks == nil {
			checks = []shared.Check{}
		}
		out[i] = statusJSON{PRJSON: shared.ToJSON(r.pr, r.repoURL), Checks: checks, ReviewDecision: reviewDecision(r)}
	}
	return out
}

// needsReview keeps, in order, the candidates waiting on the user's review: up to
// sectionLimit+1 of them, so the caller knows whether there are more.
func (s *statusRun) needsReview(ctx context.Context, prs []git.GitPullRequest) ([]git.GitPullRequest, error) {
	var groupIDs []string
	for _, pr := range prs {
		for _, rv := range reviewers(pr) {
			if id := strings.ToLower(shared.Deref(rv.Id)); shared.Deref(rv.IsContainer) && !slices.Contains(groupIDs, id) {
				groupIDs = append(groupIDs, id)
			}
		}
	}
	groups, err := s.resolver.MemberGroups(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	var out []git.GitPullRequest
	for _, pr := range prs {
		want, voted := awaitsReview(pr, s.me, groups)
		if !want && len(voted) > 0 {
			if want, err = s.requiredPending(ctx, pr, voted); err != nil {
				return nil, err
			}
		}
		if want {
			if out = append(out, pr); len(out) > sectionLimit {
				break
			}
		}
	}
	return out, nil
}

// awaitsReview reports whether pr waits on me: I'm not its author and either I'm a reviewer
// who hasn't voted or declined, or (not being one) a group of mine is an unvoted reviewer.
// voted lists the required groups of mine whose rolled-up vote is set: they wait until their
// policy passes. Optional groups stop waiting once any member voted for them.
func awaitsReview(pr git.GitPullRequest, me string, groups map[string]bool) (want bool, voted []git.IdentityRefWithVote) {
	if pr.CreatedBy != nil && strings.EqualFold(shared.Deref(pr.CreatedBy.Id), me) {
		return false, nil
	}
	rvs := reviewers(pr)
	for _, rv := range rvs {
		if strings.EqualFold(shared.Deref(rv.Id), me) {
			return shared.Deref(rv.Vote) == 0 && !shared.Deref(rv.HasDeclined), nil
		}
	}
	for _, rv := range rvs {
		if !groups[strings.ToLower(shared.Deref(rv.Id))] || shared.Deref(rv.HasDeclined) {
			continue
		}
		if shared.Deref(rv.Vote) == 0 {
			return true, nil
		}
		if shared.Deref(rv.IsRequired) {
			voted = append(voted, rv)
		}
	}
	return false, voted
}

// requiredPending reports whether a required reviewer policy naming one of groups hasn't
// passed; a group no policy names (made required by hand) waits until it approves.
func (s *statusRun) requiredPending(ctx context.Context, pr git.GitPullRequest, groups []git.IdentityRefWithVote) (bool, error) {
	evals, err := shared.Evaluations(ctx, s.c, &pr)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		named := false
		for _, e := range evals {
			if shared.PolicyType(e) != "requiredReviewers" {
				continue
			}
			settings, _ := e.Configuration.Settings.(map[string]any)
			ids, _ := settings["requiredReviewerIds"].([]any)
			if !slices.ContainsFunc(ids, func(id any) bool { s, _ := id.(string); return strings.EqualFold(s, shared.Deref(g.Id)) }) {
				continue
			}
			named = true
			if shared.Deref(e.Status) != "approved" {
				return true, nil
			}
		}
		if !named && shared.Deref(g.Vote) < 5 {
			return true, nil
		}
	}
	return false, nil
}

func reviewers(pr git.GitPullRequest) []git.IdentityRefWithVote {
	if pr.Reviewers == nil {
		return nil
	}
	return *pr.Reviewers
}

// reviewDecision is changesRequested (a reject or wait-for-author vote), reviewRequired (a
// required reviewer or reviewer policy not approved), approved (an approving vote) or "".
func reviewDecision(r row) string {
	required, approved := false, false
	for _, rv := range reviewers(r.pr) {
		vote := shared.Deref(rv.Vote)
		if vote < 0 {
			return "changesRequested"
		}
		required = required || shared.Deref(rv.IsRequired) && vote < 5
		approved = approved || vote >= 5
	}
	for _, ch := range r.checks {
		if (ch.Type == "minimumReviewers" || ch.Type == "requiredReviewers") && ch.Bucket != "pass" {
			required = true
		}
	}
	switch {
	case required:
		return "reviewRequired"
	case approved:
		return "approved"
	}
	return ""
}

func (s *statusRun) printSection(w io.Writer, cs *iostreams.ColorScheme, title string, rows []row, more bool, empty string) {
	fmt.Fprintln(w, cs.Bold(title))
	if len(rows) == 0 {
		fmt.Fprintln(w, cs.Gray("  "+empty))
	} else {
		s.printRows(w, cs, rows, more)
	}
	fmt.Fprintln(w)
}

// printRows prints gh's rows: "#N  <title> [<branch>]" plus a draft marker, then for active
// pull requests a line with the checks, review and (with -c) merge state.
func (s *statusRun) printRows(w io.Writer, cs *iostreams.ColorScheme, rows []row, more bool) {
	for _, r := range rows {
		pr := r.pr
		status, draft := shared.Deref(pr.Status), shared.Deref(pr.IsDraft)
		color := cs.Green
		switch {
		case status == git.PullRequestStatusValues.Completed:
			color = cs.Magenta
		case status == git.PullRequestStatusValues.Abandoned:
			color = cs.Red
		case draft:
			color = cs.Gray
		}
		branch := strings.TrimPrefix(shared.Deref(pr.SourceRefName), "refs/heads/")
		fmt.Fprintf(w, "  %s  %s %s", color(fmt.Sprintf("%s#%d", r.repoName, shared.Deref(pr.PullRequestId))),
			text.Truncate(50, strings.Join(strings.Fields(shared.Deref(pr.Title)), " ")), cs.Cyan("["+branch+"]"))
		if draft {
			fmt.Fprint(w, " "+cs.Gray("(draft)"))
		}
		if status != git.PullRequestStatusValues.Active {
			fmt.Fprintf(w, " - %s\n", color(strings.ToUpper(string(status[:1]))+string(status[1:])))
			continue
		}
		var parts []string
		if summary := checksSummary(cs, shared.TallyChecks(r.checks)); summary != "" {
			parts = append(parts, summary)
		}
		switch reviewDecision(r) {
		case "changesRequested":
			parts = append(parts, cs.Red("+ Changes requested"))
		case "reviewRequired":
			parts = append(parts, cs.Yellow("- Review required"))
		case "approved":
			n := 0
			for _, rv := range reviewers(pr) {
				if shared.Deref(rv.Vote) >= 5 && !shared.Deref(rv.IsContainer) {
					n++
				}
			}
			parts = append(parts, cs.Green(fmt.Sprintf("✓ %d Approved", max(n, 1))))
		}
		if s.o.conflictStatus {
			switch shared.Deref(pr.MergeStatus) {
			case git.PullRequestAsyncStatusValues.Succeeded:
				parts = append(parts, cs.Green("✓ No merge conflicts"))
			case git.PullRequestAsyncStatusValues.Conflicts:
				parts = append(parts, cs.Red("× Merge conflicts"))
			default:
				parts = append(parts, cs.Yellow("! Merge conflict status unknown"))
			}
		}
		if len(parts) > 0 {
			fmt.Fprint(w, "\n  "+strings.Join(parts, " "))
		}
		fmt.Fprintln(w)
	}
	if more {
		fmt.Fprintln(w, cs.Gray("  And more"))
	}
}

// checksSummary is gh's one-line checks summary of the gating checks; "" without checks.
func checksSummary(cs *iostreams.ColorScheme, n shared.CheckCounts) string {
	total := n.Pass + n.Fail + n.Pending + n.Skipping
	switch {
	case total == 0:
		return ""
	case n.Fail == total:
		return cs.Red("× All checks failing")
	case n.Fail > 0:
		return cs.Red(fmt.Sprintf("× %d/%d checks failing", n.Fail, total))
	case n.Pending > 0:
		return cs.Yellow("- Checks pending")
	}
	return cs.Green("✓ Checks passing")
}
